# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## プロジェクト概要

Goで実装されたディレクトリスキャン並列化ベンチマークツール。異なるディレクトリ構造と並列化戦略での性能を測定し比較します。

## 開発コマンド

### ビルドと実行

```bash
# 通常ビルド
go build

# 開発モード実行（小規模データ: 16-32ファイル）
go run main.go dev
# または
go run . dev

# 本番モード実行（大規模データ: 10,000ファイル×2構造）
go run main.go
# または
./go-parallel-dir-scan-benchmark  # ビルド後

# プロファイル取得付き実行
go run main.go -cpuprofile=prof/cpu.prof
go run main.go -memprofile=prof/mem.prof
```

### プロファイル解析

```bash
# CPUプロファイル解析
go tool pprof prof/cpu.prof

# メモリプロファイル解析
go tool pprof prof/mem.prof
```

### 依存関係管理

```bash
# 依存関係の整理（標準ライブラリのみ使用）
go mod tidy
```

## アーキテクチャ

### 主要コンポーネント

**Scanner インターフェース**
```go
type Scanner interface {
    Scan(rootPath string) (*ScanResult, error)
}
```

並列化戦略の実装：
- `DirectoryBasedScanner`: トップレベルディレクトリを各ワーカーに静的分配
- `RecursiveTaskScanner`: タスクキューを使った動的負荷分散

### 並列処理パターン

1. **DirectoryBased戦略**
   - Worker Poolパターン: 固定数のワーカーがチャネルからディレクトリを取得
   - 各ワーカーは割り当てられたディレクトリを`filepath.Walk`で逐次処理
   - `sync.WaitGroup`でワーカーの完了を同期

2. **RecursiveTask戦略**
   - Producer-Consumerパターン: ディレクトリ発見時に新たなタスクをキューに追加
   - バッファ付きチャネル（容量1000）でタスクキュー実装
   - チャネルがフルの場合はインライン処理にフォールバック
   - `sync.WaitGroup`でタスク完了とワーカー完了を二重管理

### データ構造

**Config**: ベンチマーク設定
- 開発モード: 4×4 浅い構造、2^4 深い構造
- 本番モード: 100×100 浅い構造、10^4 深い構造

**ScanResult**: スキャン結果（atomic操作で並行安全）
```go
type ScanResult struct {
    Files int64
    Dirs  int64
}
```

## テストデータ構造

1. **浅い構造 (shallow)**
   - フラット構造: 多数の独立ディレクトリ
   - 並列化しやすい（各ディレクトリが独立）

2. **深い構造 (deep)**
   - 階層構造: 4レベルの入れ子ディレクトリ
   - 並列化の難易度が高い（依存関係あり）

## ベンチマーク実行フロー

1. テストデータ作成（`/tmp/benchmark_*`）
2. 各構造×各戦略×各ワーカー数で実行
3. 結果をCSVファイル（`benchmark/benchmark_results_*.csv`）に保存
4. コンソールに結果表示（実行時間、speedup）

## 出力ファイル

- `benchmark/*.csv`: ベンチマーク結果
- `prof/*.prof`: プロファイルデータ
- `/tmp/benchmark_*`: テストデータ（実行後も残る）

## 注意点

- `ioutil.ReadDir`と`ioutil.WriteFile`は非推奨だが使用中（Go 1.16+では`os.ReadDir`と`os.WriteFile`推奨）
- CPU監視機能（`cpu_monitor.go`）は簡易実装で実際のCPU使用率は取得していない
- テストデータは`/tmp`に作成され自動削除されない