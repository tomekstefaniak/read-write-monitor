// Package rwmonitor implements a read-write monitor using the stateful goroutine
// pattern. A single goroutine owns all shared state and processes requests via
// channels, maintaining a FIFO queue that preserves arrival order while batching
// concurrent reads.
package rwmonitor

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// ReadReq is a request to perform a read operation.
// ResChan must be a buffered channel of capacity 1.
type ReadReq[T any] struct {
	ResChan chan ReadRes[T] // Channel to send the read result
}

// ReadRes holds the result of a read operation.
type ReadRes[T any] struct {
	Data T     // Data read
	Err  error // Error encountered during read
}

// WriteReq is a request to perform a write operation.
// ResChan must be a buffered channel of capacity 1.
type WriteReq[T any] struct {
	Data    T         // Data to write
	ResChan chan error // Channel to send the write result
}

// RWMonitor manages concurrent read and write access to a resource.
// Send ReadReq or WriteReq values to RequestsChan to queue operations.
type RWMonitor[T any] struct {
	requestsQueue  *list.List           // requestsQueue of processes waiting to read or write
	RequestsChan   chan any              // Channel to receive requests
	readFunc       func() (T, error)    // Function to read data
	readersCount   int                  // Number of readers currently reading
	readersCountMu sync.Mutex           // Mutex to protect access to readers field
	writeFunc      func(T) error        // Function to write data
	writing        int32                // Atomic flag indicating an active write operation (1 = writing, 0 = idle)
	checkQueueChan chan struct{}         // Channel to check the request queue
	StopChan       chan *sync.WaitGroup // Channel to stop the RWMonitor
}

// NewRWMonitor creates a new RWMonitor with the given read and write functions.
// Call StartRWMonitor in a goroutine to begin processing requests.
func NewRWMonitor[T any](
	readFunc func() (T, error),
	writeFunc func(T) error,
) *RWMonitor[T] {
	return &RWMonitor[T]{
		requestsQueue:  list.New(),
		RequestsChan:   make(chan any),
		readFunc:       readFunc,
		writeFunc:      writeFunc,
		checkQueueChan: make(chan struct{}, 1),
		StopChan:       make(chan *sync.WaitGroup),
	}
}

// StartRWMonitor runs the monitor loop. Must be called in a goroutine.
// It processes requests from RequestsChan in FIFO order, batching consecutive
// reads and serializing writes.
func (m *RWMonitor[T]) StartRWMonitor() {
	for {
		select {

		case req := <-m.RequestsChan:
			switch req := req.(type) {

			case ReadReq[T]:
				m.readersCountMu.Lock()
				if atomic.LoadInt32(&m.writing) == 1 || m.requestsQueue.Len() > 0 {
					// If there is write in progress or queue is not empty
					m.requestsQueue.PushBack(req) // Add the read request to the queue
				} else {
					// If no write is in progress and queue is empty, read the data
					m.readersCount++       // Increment the readers count
					go m.read(req.ResChan) // Perform the read operation
				}
				m.readersCountMu.Unlock()

			case WriteReq[T]:
				m.readersCountMu.Lock()
				if m.readersCount > 0 || atomic.LoadInt32(&m.writing) == 1 || m.requestsQueue.Len() > 0 {
					// If there are readers, write is in progress or queue is not empty
					m.requestsQueue.PushBack(req) // Add the write request to the queue
				} else {
					// If no write is in progress and queue is empty, perform the write
					atomic.StoreInt32(&m.writing, 1)  // Atomic write to set writing flag
					go m.write(req.Data, req.ResChan) // Perform the write operation
				}
				m.readersCountMu.Unlock()
			}

		case <-m.checkQueueChan:
			m.readersCountMu.Lock()
			if m.readersCount > 0 || atomic.LoadInt32(&m.writing) == 1 {
				// Case when last reader finished while monitor was starting new operation
				m.readersCountMu.Unlock()
				break
			}
			m.readersCountMu.Unlock()

			wereReaders := false // Flag to indicate if there were any readers processed  in loop before
		loop:
			for m.requestsQueue.Len() > 0 {
				reqElement := m.requestsQueue.Front() // Get the first request element from the queue

				switch req := reqElement.Value.(type) {

				case ReadReq[T]: // Process all read requests until a write request is found
					m.requestsQueue.Remove(reqElement) // Remove the request from the queue AFTER we know we can process it
					m.readersCountMu.Lock()
					m.readersCount++
					m.readersCountMu.Unlock()
					go m.read(req.ResChan) // Perform the read operation
					wereReaders = true

				case WriteReq[T]: // Process the write request and break the loop
					if wereReaders {
						break loop // If there were readers processed before, break the loop to allow them to finish (keep write request in queue)
					}
					m.requestsQueue.Remove(reqElement) // Remove the request from the queue AFTER we know we can process it
					atomic.StoreInt32(&m.writing, 1)
					go m.write(req.Data, req.ResChan) // Perform the write operation
					break loop
				}
			}

		case wg := <-m.StopChan:
			// Wait for all readers and writers to finish
			m.readersCountMu.Lock()
			readersCount := m.readersCount
			m.readersCountMu.Unlock()
			writing := atomic.LoadInt32(&m.writing)
			if readersCount > 0 || writing > 0 {
				<-m.checkQueueChan
			}

			m.CleanUp()
			wg.Done()
			return
		}
	}
}

// CleanUp closes all channels and resets the monitor state.
// Called automatically by StopChan after all in-flight operations finish.
func (m *RWMonitor[T]) CleanUp() {
	// Clear the requests queue

	m.requestsQueue = list.New() // Clear the requests queue

	// Close all channels
	close(m.RequestsChan)
	close(m.checkQueueChan)
	close(m.StopChan)

	// Create new channels to allow restarting the monitor
	m.RequestsChan = make(chan any)
	m.checkQueueChan = make(chan struct{}, 1)
	m.StopChan = make(chan *sync.WaitGroup)
}

// Wrapper function for the read operation
func (m *RWMonitor[T]) read(resChan chan ReadRes[T]) {
	// Perform the read operation
	data, err := m.readFunc()
	// Non-blocking send to avoid deadlock if the receiver is not ready
	select {
	case resChan <- ReadRes[T]{Data: data, Err: err}:
	default: // If the channel is full, do nothing
	}

	// Signal that the read operation has finished
	m.readersCountMu.Lock()
	m.readersCount--
	readersCount := m.readersCount
	m.readersCountMu.Unlock()
	if readersCount == 0 {
		select {
		case m.checkQueueChan <- struct{}{}:
		default: // Don't block if channel is full
		}
	}
}

// Wrapper function for the write operation
func (m *RWMonitor[T]) write(data T, resChan chan error) {
	// Perform the write operation
	err := m.writeFunc(data)

	// Non-blocking send to avoid deadlock if the receiver is not ready
	select {
	case resChan <- err:
	default: // If the channel is full, do nothing
	}

	// Signal that the write operation has finished
	atomic.StoreInt32(&m.writing, 0)
	select {
	case m.checkQueueChan <- struct{}{}:
	default: // Don't block if channel is full
	}
}
