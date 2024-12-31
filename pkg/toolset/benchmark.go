package toolset

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
	flag "github.com/spf13/pflag"

	"github.com/iotaledger/hive.go/app/configuration"
	"github.com/iotaledger/hive.go/db"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/iota-core/pkg/storage/database"
	iotago_tpkg "github.com/iotaledger/iota.go/v4/tpkg"
)

// estimateRemainingTime estimates the remaining time for a running operation and returns the finished percentage.
func estimateRemainingTime(timeStart time.Time, current int64, total int64) (percentage float64, remaining time.Duration) {
	ratio := float64(current) / float64(total)
	totalTime := time.Duration(float64(time.Since(timeStart)) / ratio)
	remaining = time.Until(timeStart.Add(totalTime))

	return ratio * 100.0, remaining
}

func benchmarkIO(args []string) error {
	fs := configuration.NewUnsortedFlagSet("", flag.ContinueOnError)
	objectsCountFlag := fs.Int(FlagToolBenchmarkCount, 500000, "objects count")
	objectsSizeFlag := fs.Int(FlagToolBenchmarkSize, 1000, "objects size in bytes")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", ToolBenchmarkIO)
		fs.PrintDefaults()
		println(fmt.Sprintf("\nexample: %s --%s %d --%s %d",
			ToolBenchmarkIO,
			FlagToolBenchmarkCount,
			500000,
			FlagToolBenchmarkSize,
			1000))
	}

	if err := parseFlagSet(fs, args); err != nil {
		return err
	}

	objectCnt := *objectsCountFlag
	size := *objectsSizeFlag

	tempDir, err := os.MkdirTemp("", "benchmarkIO")
	if err != nil {
		return fmt.Errorf("can't create temp dir: %w", err)
	}

	defer func() { _ = os.RemoveAll(tempDir) }()

	store, err := database.StoreWithDefaultSettings(tempDir, true, db.EngineRocksDB, db.EngineRocksDB)
	if err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}

	batchWriter := kvstore.NewBatchedWriter(store)
	writeDoneWaitGroup := &sync.WaitGroup{}
	writeDoneWaitGroup.Add(objectCnt)

	ts := time.Now()

	lastStatusTime := time.Now()
	for i := range objectCnt {
		// one read operation and one write operation per cycle
		batchWriter.Enqueue(newBenchmarkObject(store, writeDoneWaitGroup, iotago_tpkg.RandBytes(32), iotago_tpkg.RandBytes(size)))

		if time.Since(lastStatusTime) >= printStatusInterval {
			lastStatusTime = time.Now()

			duration := time.Since(ts)
			bytes := uint64(i * (32 + size))
			totalBytes := uint64(objectCnt * (32 + size))
			bytesPerSecond := uint64(float64(bytes) / duration.Seconds())
			objectsPerSecond := uint64(float64(i) / duration.Seconds())
			percentage, remaining := estimateRemainingTime(ts, int64(i), int64(objectCnt))
			fmt.Printf("Average IO speed: %s/s (%dx 32+%d byte chunks, total %s/%s, %d objects/s, %0.2f%%. %v left ...)\n", humanize.Bytes(bytesPerSecond), i, size, humanize.Bytes(bytes), humanize.Bytes(totalBytes), objectsPerSecond, percentage, remaining.Truncate(time.Second))
		}
	}

	writeDoneWaitGroup.Wait()

	if err := store.Flush(); err != nil {
		return fmt.Errorf("flush database failed: %w", err)
	}

	if err := store.Close(); err != nil {
		return fmt.Errorf("close database failed: %w", err)
	}

	te := time.Now()
	duration := te.Sub(ts)
	totalBytes := uint64(objectCnt * (32 + size))
	bytesPerSecond := uint64(float64(totalBytes) / duration.Seconds())
	objectsPerSecond := uint64(float64(objectCnt) / duration.Seconds())

	fmt.Printf("Average IO speed: %s/s (%dx 32+%d byte chunks, total %s/%s, %d objects/s, took %v)\n", humanize.Bytes(bytesPerSecond), objectCnt, size, humanize.Bytes(totalBytes), humanize.Bytes(totalBytes), objectsPerSecond, duration.Truncate(time.Millisecond))

	return nil
}

func benchmarkCPU(args []string) error {
	fs := configuration.NewUnsortedFlagSet("", flag.ContinueOnError)
	cpuThreadsFlag := fs.Int(FlagToolBenchmarkThreads, runtime.NumCPU(), "thread count")
	durationFlag := fs.Duration(FlagToolBenchmarkDuration, 1*time.Minute, "duration")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", ToolBenchmarkCPU)
		fs.PrintDefaults()
		println(fmt.Sprintf("\nexample: %s --%s %d --%s 1m0s",
			ToolBenchmarkCPU,
			FlagToolBenchmarkThreads,
			2,
			FlagToolBenchmarkDuration))
	}

	if err := parseFlagSet(fs, args); err != nil {
		return err
	}

	threads := *cpuThreadsFlag
	duration := *durationFlag

	benchmarkCtx, benchmarkCtxCancel := context.WithTimeout(context.Background(), duration)
	defer benchmarkCtxCancel()

	ts := time.Now()

	// doBenchmarkCPU mines with blake2b until the context has been canceled.
	// it returns the number of calculated hashes.
	doBenchmarkCPU := func(ctx context.Context, numWorkers int) uint64 {
		var counter uint64
		var wg sync.WaitGroup

		// random digest
		powDigest := iotago_tpkg.RandBytes(32)

		go func() {
			for ctx.Err() == nil {
				time.Sleep(printStatusInterval)

				elapsed := time.Since(ts)
				percentage, remaining := estimateRemainingTime(ts, elapsed.Milliseconds(), duration.Milliseconds())
				megahashesPerSecond := float64(counter) / (elapsed.Seconds() * 1000000)
				fmt.Printf("Average CPU speed: %0.2fMH/s (%d thread(s), %0.2f%%. %v left ...)\n", megahashesPerSecond, numWorkers, percentage, remaining.Truncate(time.Second))
			}
		}()

		for range numWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()

				cpuBenchmarkWorker(ctx, powDigest, &counter)
			}()
		}
		wg.Wait()

		return counter
	}

	hashes := doBenchmarkCPU(benchmarkCtx, threads)
	megahashesPerSecond := float64(hashes) / (duration.Seconds() * 1000000)
	fmt.Printf("Average CPU speed: %0.2fMH/s (%d thread(s), took %v)\n", megahashesPerSecond, threads, duration.Truncate(time.Millisecond))

	return nil
}
