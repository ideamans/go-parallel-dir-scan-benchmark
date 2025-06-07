package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Configuration
type Config struct {
	IsDevelopment    bool
	ShallowDirs      int
	ShallowFiles     int
	DeepLevels       int
	DeepDirsPerLevel int
}

// BenchmarkResult holds benchmark results
type BenchmarkResult struct {
	Structure    string
	Strategy     string
	Workers      int
	Duration     time.Duration
	FilesScanned int
	DirsScanned  int
	Speedup      float64
}

// Directory structure types
const (
	StructureShallow = "shallow"
	StructureDeep    = "deep"
)

// Parallelization strategies
const (
	StrategyDirectoryBased = "directory-based"
	StrategyRecursiveTask  = "recursive-task"
)

// getConfig returns configuration based on development mode
func getConfig(isDev bool) Config {
	if isDev {
		return Config{
			IsDevelopment:    true,
			ShallowDirs:      4,
			ShallowFiles:     4,
			DeepLevels:       4,
			DeepDirsPerLevel: 2,
		}
	}
	return Config{
		IsDevelopment:    false,
		ShallowDirs:      100,
		ShallowFiles:     100,
		DeepLevels:       4,
		DeepDirsPerLevel: 10,
	}
}

// createShallowStructure creates a shallow directory structure
func createShallowStructure(rootPath string, config Config) error {
	for i := 0; i < config.ShallowDirs; i++ {
		dirPath := filepath.Join(rootPath, fmt.Sprintf("dir_%03d", i))
		if err := os.Mkdir(dirPath, 0755); err != nil {
			return err
		}

		for j := 0; j < config.ShallowFiles; j++ {
			filePath := filepath.Join(dirPath, fmt.Sprintf("file_%03d.txt", j))
			content := []byte(fmt.Sprintf("File %d in directory %d", j, i))
			if err := ioutil.WriteFile(filePath, content, 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

// createDeepStructure creates a deep directory structure recursively
func createDeepStructure(rootPath string, config Config) error {
	var createLevel func(path string, level int) error

	createLevel = func(path string, level int) error {
		if level >= config.DeepLevels {
			// Create files at the deepest level
			for i := 0; i < config.DeepDirsPerLevel; i++ {
				filePath := filepath.Join(path, fmt.Sprintf("file_%03d.txt", i))
				content := []byte(fmt.Sprintf("File at level %d", level))
				if err := ioutil.WriteFile(filePath, content, 0644); err != nil {
					return err
				}
			}
			return nil
		}

		// Create directories and recurse
		for i := 0; i < config.DeepDirsPerLevel; i++ {
			dirPath := filepath.Join(path, fmt.Sprintf("level%d_dir%03d", level, i))
			if err := os.Mkdir(dirPath, 0755); err != nil {
				return err
			}
			if err := createLevel(dirPath, level+1); err != nil {
				return err
			}
		}
		return nil
	}

	return createLevel(rootPath, 0)
}

// ScanResult holds the scan results
type ScanResult struct {
	Files int64
	Dirs  int64
}

// DirectoryBasedScanner implements directory-based parallel scanning
type DirectoryBasedScanner struct {
	numWorkers int
}

func (s *DirectoryBasedScanner) Scan(rootPath string) (*ScanResult, error) {
	result := &ScanResult{}

	if s.numWorkers == 1 {
		return s.scanSerial(rootPath)
	}

	// Get top-level directories
	entries, err := ioutil.ReadDir(rootPath)
	if err != nil {
		return nil, err
	}

	dirChan := make(chan string, len(entries))
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < s.numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for dirPath := range dirChan {
				localResult, err := s.scanSerial(dirPath)
				if err != nil {
					fmt.Printf("Error scanning %s: %v\n", dirPath, err)
					continue
				}
				atomic.AddInt64(&result.Files, localResult.Files)
				atomic.AddInt64(&result.Dirs, localResult.Dirs)
			}
		}()
	}

	// Count root directory and process root-level entries
	atomic.AddInt64(&result.Dirs, 1)

	// Queue directories and count root-level files
	for _, entry := range entries {
		if entry.IsDir() {
			dirChan <- filepath.Join(rootPath, entry.Name())
		} else {
			atomic.AddInt64(&result.Files, 1)
		}
	}
	close(dirChan)

	wg.Wait()

	return result, nil
}

func (s *DirectoryBasedScanner) scanSerial(path string) (*ScanResult, error) {
	result := &ScanResult{}

	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			result.Dirs++
		} else {
			result.Files++
		}
		return nil
	})

	return result, err
}

// RecursiveTaskScanner implements recursive task-based parallel scanning
type RecursiveTaskScanner struct {
	numWorkers int
}

func (s *RecursiveTaskScanner) Scan(rootPath string) (*ScanResult, error) {
	result := &ScanResult{}

	if s.numWorkers == 1 {
		return s.scanSerialRecursive(rootPath)
	}

	// Use a buffered channel for tasks
	taskChan := make(chan string, 1000)
	var wg sync.WaitGroup
	var taskWg sync.WaitGroup

	// Start workers
	wg.Add(s.numWorkers)
	for i := 0; i < s.numWorkers; i++ {
		go func() {
			defer wg.Done()
			for path := range taskChan {
				s.processPath(path, taskChan, &taskWg, result)
				taskWg.Done()
			}
		}()
	}

	// Add initial task
	taskWg.Add(1)
	taskChan <- rootPath

	// Wait for all tasks to complete
	taskWg.Wait()
	close(taskChan)

	// Wait for all workers to finish
	wg.Wait()

	return result, nil
}

func (s *RecursiveTaskScanner) processPath(path string, taskChan chan<- string, taskWg *sync.WaitGroup, result *ScanResult) {
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		fmt.Printf("Error reading %s: %v\n", path, err)
		return
	}

	atomic.AddInt64(&result.Dirs, 1)

	for _, entry := range entries {
		if entry.IsDir() {
			fullPath := filepath.Join(path, entry.Name())
			// Try to add task to channel
			select {
			case taskChan <- fullPath:
				taskWg.Add(1)
			default:
				// Channel full, process inline
				s.processPathRecursive(fullPath, result)
			}
		} else {
			atomic.AddInt64(&result.Files, 1)
		}
	}
}

func (s *RecursiveTaskScanner) processPathRecursive(path string, result *ScanResult) {
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		return
	}

	atomic.AddInt64(&result.Dirs, 1)

	for _, entry := range entries {
		if entry.IsDir() {
			s.processPathRecursive(filepath.Join(path, entry.Name()), result)
		} else {
			atomic.AddInt64(&result.Files, 1)
		}
	}
}

func (s *RecursiveTaskScanner) scanSerialRecursive(path string) (*ScanResult, error) {
	result := &ScanResult{}
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			result.Dirs++
		} else {
			result.Files++
		}
		return nil
	})
	return result, err
}

// runBenchmark executes a single benchmark
func runBenchmark(rootPath, structure, strategy string, numWorkers int) (*BenchmarkResult, error) {
	start := time.Now()

	var scanner interface {
		Scan(string) (*ScanResult, error)
	}

	switch strategy {
	case StrategyDirectoryBased:
		scanner = &DirectoryBasedScanner{numWorkers: numWorkers}
	case StrategyRecursiveTask:
		scanner = &RecursiveTaskScanner{numWorkers: numWorkers}
	default:
		return nil, fmt.Errorf("unknown strategy: %s", strategy)
	}

	result, err := scanner.Scan(rootPath)
	if err != nil {
		return nil, err
	}

	duration := time.Since(start)

	return &BenchmarkResult{
		Structure:    structure,
		Strategy:     strategy,
		Workers:      numWorkers,
		Duration:     duration,
		FilesScanned: int(result.Files),
		DirsScanned:  int(result.Dirs),
	}, nil
}

// exportResultsToCSV exports results to CSV file
func exportResultsToCSV(results []BenchmarkResult, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Header
	writer.Write([]string{"Structure", "Strategy", "Workers", "Duration_ms", "Files", "Dirs", "Speedup"})

	// Data
	for _, r := range results {
		writer.Write([]string{
			r.Structure,
			r.Strategy,
			fmt.Sprintf("%d", r.Workers),
			fmt.Sprintf("%.2f", r.Duration.Seconds()*1000),
			fmt.Sprintf("%d", r.FilesScanned),
			fmt.Sprintf("%d", r.DirsScanned),
			fmt.Sprintf("%.2f", r.Speedup),
		})
	}

	return nil
}

func main() {
	var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")
	var memprofile = flag.String("memprofile", "", "write memory profile to file")
	flag.Parse()

	// Setup CPU profiling
	if *cpuprofile != "" {
		// Create prof directory if not exists
		profDir := filepath.Dir(*cpuprofile)
		if profDir != "." && profDir != "" {
			if err := os.MkdirAll(profDir, 0755); err != nil {
				fmt.Printf("プロファイルディレクトリ作成エラー: %v\n", err)
				os.Exit(1)
			}
		}
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Printf("CPUプロファイル作成エラー: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Printf("CPUプロファイル開始エラー: %v\n", err)
			os.Exit(1)
		}
		defer pprof.StopCPUProfile()
	}

	// Check command line arguments
	isDev := false
	for _, arg := range flag.Args() {
		if arg == "dev" {
			isDev = true
			break
		}
	}
	config := getConfig(isDev)

	fmt.Println("ディレクトリスキャン並列化ベンチマーク")
	fmt.Printf("モード: %s\n", map[bool]string{true: "開発", false: "本番"}[isDev])
	fmt.Printf("CPU数: %d\n", runtime.NumCPU())
	fmt.Println("=====================================")

	// Setup test data
	testDirs := map[string]string{
		StructureShallow: "benchmark_shallow",
		StructureDeep:    "benchmark_deep",
	}

	// Create test data
	for structure, dirPath := range testDirs {
		fmt.Printf("\n%s構造のテストデータを作成中...\n", structure)
		os.RemoveAll(dirPath)
		if err := os.Mkdir(dirPath, 0755); err != nil {
			fmt.Printf("エラー: %v\n", err)
			return
		}

		var err error
		switch structure {
		case StructureShallow:
			err = createShallowStructure(dirPath, config)
		case StructureDeep:
			err = createDeepStructure(dirPath, config)
		}

		if err != nil {
			fmt.Printf("エラー: %v\n", err)
			return
		}

		// Verify file count
		var expectedFiles int
		if structure == StructureShallow {
			expectedFiles = config.ShallowDirs * config.ShallowFiles
		} else {
			// For deep structure: files are only at the deepest level
			// Number of leaf directories = dirsPerLevel^levels
			// Files per leaf directory = dirsPerLevel
			leafDirs := 1
			for i := 0; i < config.DeepLevels; i++ {
				leafDirs *= config.DeepDirsPerLevel
			}
			expectedFiles = leafDirs * config.DeepDirsPerLevel
		}
		fmt.Printf("期待されるファイル数: %d\n", expectedFiles)
	}

	// Run benchmarks
	strategies := []string{StrategyDirectoryBased, StrategyRecursiveTask}
	workerCounts := []int{1, 2, 4, 8}
	results := []BenchmarkResult{}

	fmt.Println("\n===== ベンチマーク実行 =====")

	for structure, dirPath := range testDirs {
		fmt.Printf("\n構造: %s\n", structure)

		for _, strategy := range strategies {
			fmt.Printf("\n戦略: %s\n", strategy)

			// Store baseline for speedup calculation
			var baselineDuration time.Duration

			for _, workers := range workerCounts {
				fmt.Printf("  ワーカー数 %d でベンチマーク実行中...", workers)

				// Run multiple times and take average
				const numRuns = 3
				var totalDuration time.Duration
				var result *BenchmarkResult

				for i := 0; i < numRuns; i++ {
					r, err := runBenchmark(dirPath, structure, strategy, workers)
					if err != nil {
						fmt.Printf("\n  エラー: %v\n", err)
						break
					}
					totalDuration += r.Duration
					result = r
				}

				if result != nil {
					result.Duration = totalDuration / numRuns

					// Calculate speedup
					if workers == 1 {
						baselineDuration = result.Duration
						result.Speedup = 1.0
					} else {
						result.Speedup = float64(baselineDuration) / float64(result.Duration)
					}

					results = append(results, *result)

					// Verify file count
					var expectedFiles int
					if structure == StructureShallow {
						expectedFiles = config.ShallowDirs * config.ShallowFiles
					} else {
						// For deep structure: files are only at the deepest level
						// Number of leaf directories = dirsPerLevel^levels
						// Files per leaf directory = dirsPerLevel
						leafDirs := 1
						for i := 0; i < config.DeepLevels; i++ {
							leafDirs *= config.DeepDirsPerLevel
						}
						expectedFiles = leafDirs * config.DeepDirsPerLevel
					}

					if result.FilesScanned != expectedFiles {
						fmt.Printf(" 警告: ファイル数が一致しません (期待: %d, 実際: %d)",
							expectedFiles, result.FilesScanned)
					}
					fmt.Printf(" 完了 (%.3fs, speedup: %.2fx)\n",
						result.Duration.Seconds(), result.Speedup)
				}
			}
		}
	}

	// Display results
	fmt.Println("\n===== ベンチマーク結果サマリー =====")
	fmt.Printf("%-10s %-20s %-8s %-12s %-10s %-10s %-10s\n",
		"Structure", "Strategy", "Workers", "Duration", "Files", "Dirs", "Speedup")
	fmt.Println(strings.Repeat("-", 80))

	for _, result := range results {
		fmt.Printf("%-10s %-20s %-8d %-12s %-10d %-10d %-10.2fx\n",
			result.Structure,
			result.Strategy,
			result.Workers,
			result.Duration.Round(time.Millisecond),
			result.FilesScanned,
			result.DirsScanned,
			result.Speedup)
	}

	// Export to CSV
	// Create benchmark directory if not exists
	benchmarkDir := "benchmark"
	if err := os.MkdirAll(benchmarkDir, 0755); err != nil {
		fmt.Printf("\nベンチマークディレクトリ作成エラー: %v\n", err)
	} else {
		csvFilename := fmt.Sprintf("%s/benchmark_results_%s.csv",
			benchmarkDir,
			time.Now().Format("20060102_150405"))
		if err := exportResultsToCSV(results, csvFilename); err != nil {
			fmt.Printf("\nCSV出力エラー: %v\n", err)
		} else {
			fmt.Printf("\n結果をCSVファイルに出力しました: %s\n", csvFilename)
		}
	}

	// Cleanup
	fmt.Println("\nテストデータを削除中...")
	for _, dirPath := range testDirs {
		os.RemoveAll(dirPath)
	}
	fmt.Println("完了")

	// Write memory profile if requested
	if *memprofile != "" {
		// Create prof directory if not exists
		profDir := filepath.Dir(*memprofile)
		if profDir != "." && profDir != "" {
			if err := os.MkdirAll(profDir, 0755); err != nil {
				fmt.Printf("プロファイルディレクトリ作成エラー: %v\n", err)
				return
			}
		}
		f, err := os.Create(*memprofile)
		if err != nil {
			fmt.Printf("メモリプロファイル作成エラー: %v\n", err)
			return
		}
		defer f.Close()
		runtime.GC() // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			fmt.Printf("メモリプロファイル書き込みエラー: %v\n", err)
		}
	}
}
