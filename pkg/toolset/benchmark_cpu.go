package toolset

import (
	"context"
	"sync/atomic"

	"golang.org/x/crypto/blake2b"
)

func cpuBenchmarkWorker(ctx context.Context, powDigest []byte, counter *uint64) {
	for {
		select {
		case <-ctx.Done():
			return

		default:
			result := blake2b.Sum256(powDigest)
			powDigest = result[:]
			atomic.AddUint64(counter, 1)
		}
	}
}
