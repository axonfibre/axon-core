//go:build dockertests

package dockertestframework

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ierrors"
)

// EventuallyWithDurations asserts that given condition will be met in deadline time,
// periodically checking target function each tick.
func (d *DockerTestFramework) EventuallyWithDurations(condition func() error, deadline time.Duration, tick time.Duration, waitForSync ...bool) {
	ch := make(chan error, 1)

	timer := time.NewTimer(deadline)
	defer timer.Stop()

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	var lastErr error
	for tick := ticker.C; ; {
		select {
		case <-timer.C:
			require.FailNow(d.Testing, "condition never satisfied", lastErr)
		case <-tick:
			tick = nil
			go func() { ch <- condition() }()
		case lastErr = <-ch:
			if lastErr == nil {
				// The condition is satisfied, we can exit.
				return
			}
			tick = ticker.C
		}
	}
}

// Eventually asserts that given condition will be met in opts.waitFor time,
// periodically checking target function each opts.tick.
//
//	assert.Eventually(t, func() bool { return true; }, time.Second, 10*time.Millisecond)
func (d *DockerTestFramework) Eventually(condition func() error, waitForSync ...bool) {
	deadline := d.optsWaitFor
	if len(waitForSync) > 0 && waitForSync[0] {
		deadline = d.optsWaitForSync
	}

	d.EventuallyWithDurations(condition, deadline, d.optsTick)
}

func CreateLogDirectory(testName string) string {
	// make sure logs/ exists
	err := os.Mkdir("logs", 0755)
	if err != nil {
		if !os.IsExist(err) {
			panic(err)
		}
	}

	// create directory for this run
	timestamp := time.Now().Format("20060102_150405")
	dir := fmt.Sprintf("logs/%s-%s", timestamp, testName)
	err = os.Mkdir(dir, 0755)
	if err != nil {
		if !os.IsExist(err) {
			panic(err)
		}
	}

	return dir
}

func IsStatusCode(err error, status int) bool {
	if err == nil {
		return false
	}
	code, err := ExtractStatusCode(err.Error())
	if err != nil {
		return false
	}

	return code == status
}

func ExtractStatusCode(errorMessage string) (int, error) {
	re := regexp.MustCompile(`code=(\d+)`)
	matches := re.FindStringSubmatch(errorMessage)
	if len(matches) != 2 {
		return 0, ierrors.Errorf("unable to extract status code from error message")
	}

	statusCode, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, err
	}

	return statusCode, nil
}
