package rwmonitor

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
	"sync/atomic"
)

const testGoroutines = 20
const testTTL = 20

func Logger(ch chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for msg := range ch {
		fmt.Println(msg)
	}
}

func TestRWMonitor(t *testing.T) { // Data that will be read and written that is "not prepared" for race condtions
	vulnerable := 0

	// Channel for logging
	logChan := make(chan string, testGoroutines*testTTL*2) // Bigger buffer for all logs
	var loggerWG sync.WaitGroup
	var operationsWG sync.WaitGroup

	loggerWG.Add(1)
	go Logger(logChan, &loggerWG)

	// Atomic counters to track active readers and writers for logging purposes
	var activeReaders atomic.Int32
	var activeWriter atomic.Int32

	// Example read function
	unsafeReadFunc := func() (int, error) {
		defer operationsWG.Done()
		activeReaders.Add(1)
		defer activeReaders.Add(-1)
		if activeWriter.Load() > 0 {
			t.Error("Read operation started while a write operation is active")
		}

		id := rand.Intn(int(^uint32(0))) // Generate a random ID for the reader to identify it on the logs
		logChan <- fmt.Sprintf("\033[38;5;214m<< %x started  reading\033[0m", id)

		data := vulnerable // Read the data from the vulnerable variable

		time.Sleep(time.Duration((rand.Intn(10) + 1) * int(time.Millisecond))) // Simulate some delay 1-10ms
		logChan <- fmt.Sprintf("\033[38;5;214m<< %x finished reading: %x\033[0m", id, data)
		return data, nil
	}

	// Example write function
	unsafeWriteFunc := func(data int) error {
		defer operationsWG.Done()
		activeWriter.Add(1)
		defer activeWriter.Add(-1)
		if activeReaders.Load() > 0 {
			t.Error("Write operation started while read operations are active")
		}

		id := rand.Intn(int(^uint32(0))) // Generate a random ID for the reader to identify it on the logs
		logChan <- fmt.Sprintf("\033[38;5;45m>> %x started  writing\033[0m", id)

		vulnerable = id // Write the data to the vulnerable variable

		time.Sleep(time.Duration((rand.Intn(10) + 11) * int(time.Millisecond))) // Simulate some delay 11-20ms
		logChan <- fmt.Sprintf("\033[38;5;45m>> %x finished writing\033[0m", id)
		return nil
	}

	// Stress test the RWMonitor with multiple readers and writers
	monitor := NewRWMonitor(unsafeReadFunc, unsafeWriteFunc)
	go monitor.StartRWMonitor()

	operationsWG.Add(testGoroutines * testTTL)

	for i := 0; i < testGoroutines; i++ {
		go func(id int) {
			for ttl := testTTL; ttl > 0; ttl-- {
				if rand.Intn(10) != 0 { // Read operation (90% chance)
					resChan := make(chan ReadRes[int], 1)
					monitor.RequestsChan <- ReadReq[int]{ResChan: resChan} // Send a read request
					<-resChan                                              // Wait for the read result

				} else { // Write operation (10% chance)
					resChan := make(chan error, 1)
					monitor.RequestsChan <- WriteReq[int]{Data: id, ResChan: resChan} // Send a write request
					<-resChan                                                         // Wait for the write result
				}

				time.Sleep(time.Duration((rand.Intn(19) + 1) * int(time.Millisecond))) // Simulate some delay 1-20ms
			}
		}(i)
	}

	operationsWG.Wait()
	fmt.Println("\033[38;5;48mStress test completed!\033[0m")

	// Close the log channel to signal the logger to finish
	close(logChan)

	// Wait for the logger to finish printing all messages
	loggerWG.Wait()

	// Wait a moment to ensure all read and write operations are done
	// It is made because sometimes read and write operations are still in progress
	// which causes Go Analyser to report race condition in CleanUp method even though
	// CleanUp waits for all operations to complete.
	// Thanks to this wait no falsy warnings are reported.
	time.Sleep(100 * time.Millisecond)

	// Stop the RWMonitor
	var wgStop sync.WaitGroup
	wgStop.Add(1)
	monitor.StopChan <- &wgStop
	wgStop.Wait()
	fmt.Println("\033[38;5;48mRWMonitor stopped!\033[0m")
}
