package main

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
)

// CPUMonitor はCPU使用率を監視
type CPUMonitor struct {
	startTime    time.Time
	startCPUTime time.Duration
	samples      []float64
	done         int32
}

// NewCPUMonitor は新しいCPUモニターを作成
func NewCPUMonitor() *CPUMonitor {
	return &CPUMonitor{
		startTime:    time.Now(),
		startCPUTime: getCPUTime(),
		samples:      make([]float64, 0),
	}
}

// Start はバックグラウンドでCPU使用率の監視を開始
func (m *CPUMonitor) Start() {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		lastTime := m.startTime
		lastCPUTime := m.startCPUTime

		for {
			select {
			case <-ticker.C:
				if atomic.LoadInt32(&m.done) == 1 {
					return
				}

				currentTime := time.Now()
				currentCPUTime := getCPUTime()

				elapsed := currentTime.Sub(lastTime)
				cpuElapsed := currentCPUTime - lastCPUTime

				usage := float64(cpuElapsed) / float64(elapsed) * 100
				m.samples = append(m.samples, usage)

				lastTime = currentTime
				lastCPUTime = currentCPUTime
			}
		}
	}()
}

// Stop は監視を停止
func (m *CPUMonitor) Stop() {
	atomic.StoreInt32(&m.done, 1)
	time.Sleep(200 * time.Millisecond) // 最後のサンプルを待つ
}

// GetAverageCPUUsage は平均CPU使用率を返す
func (m *CPUMonitor) GetAverageCPUUsage() float64 {
	if len(m.samples) == 0 {
		return 0
	}

	var sum float64
	for _, sample := range m.samples {
		sum += sample
	}
	return sum / float64(len(m.samples))
}

// GetMaxCPUUsage は最大CPU使用率を返す
func (m *CPUMonitor) GetMaxCPUUsage() float64 {
	if len(m.samples) == 0 {
		return 0
	}

	max := m.samples[0]
	for _, sample := range m.samples[1:] {
		if sample > max {
			max = sample
		}
	}
	return max
}

// GetStats は統計情報を返す
func (m *CPUMonitor) GetStats() CPUStats {
	return CPUStats{
		Average:     m.GetAverageCPUUsage(),
		Max:         m.GetMaxCPUUsage(),
		SampleCount: len(m.samples),
	}
}

// CPUStats はCPU使用率の統計情報
type CPUStats struct {
	Average     float64
	Max         float64
	SampleCount int
}

// getCPUTime は現在のプロセスのCPU時間を取得（簡易版）
func getCPUTime() time.Duration {
	// 実際の実装では/proc/self/statやWindows APIを使用
	// ここでは簡易的にruntime.NumGoroutine()を使用
	return time.Duration(runtime.NumGoroutine()) * time.Millisecond
}

// ExtendedBenchmarkResult は拡張されたベンチマーク結果
type ExtendedBenchmarkResult struct {
	BenchmarkResult
	CPUStats CPUStats
}

// runBenchmarkWithCPUMonitoring はCPU監視付きでベンチマークを実行
func runBenchmarkWithCPUMonitoring(rootPath, structure, strategy string, numWorkers int) (*ExtendedBenchmarkResult, error) {
	monitor := NewCPUMonitor()
	monitor.Start()

	result, err := runBenchmark(rootPath, structure, strategy, numWorkers)
	if err != nil {
		return nil, err
	}

	monitor.Stop()

	return &ExtendedBenchmarkResult{
		BenchmarkResult: *result,
		CPUStats:        monitor.GetStats(),
	}, nil
}

// 使用例
func demonstrateCPUMonitoring() {
	fmt.Println("CPU監視デモンストレーション")
	fmt.Println("============================")

	monitor := NewCPUMonitor()
	monitor.Start()

	// ベンチマークシミュレーション
	fmt.Println("処理を実行中...")
	time.Sleep(2 * time.Second)

	monitor.Stop()

	stats := monitor.GetStats()
	fmt.Printf("\n統計情報:\n")
	fmt.Printf("平均CPU使用率: %.2f%%\n", stats.Average)
	fmt.Printf("最大CPU使用率: %.2f%%\n", stats.Max)
	fmt.Printf("サンプル数: %d\n", stats.SampleCount)
}
