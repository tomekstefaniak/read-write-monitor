package rwmonitor

import (
	"container/list"
	"sync"
	"sync/atomic"
)

type ReadReq[T any] struct {
	ResChan chan ReadRes[T] // Channel to send the read result
}

type ReadRes[T any] struct {
	Data T     // Data read
	Err  error // Error encountered during read
}

type WriteReq[T any] struct {
	Data    T          // Data to write
	ResChan chan error // Channel to send the write result
}

type RWMonitor[T any] struct {
	requestsQueue  *list.List           // requestsQueue of processes waiting to read or write
	RequestsChan   chan interface{}     // Channel to receive requests
	readFunc       func() (T, error)    // Function to read data
	readersCount   int                  // Number of readers currently reading
	readersCountMu sync.Mutex           // Mutex to protect access to readers field
	writeFunc      func(T) error        // Function to write data
	writing        int32                // Change writing flag to atomic int
	checkQueueChan chan struct{}        // Channel to check the request queue
	StopChan       chan *sync.WaitGroup // Channel to stop the RWMonitor
}

func NewRWMonitor[T any](
	readFunc func() (T, error),
	writeFunc func(T) error,
) *RWMonitor[T] {
	return &RWMonitor[T]{
		requestsQueue:  list.New(),
		RequestsChan:   make(chan interface{}),
		readFunc:       readFunc,
		readersCount:   0,
		readersCountMu: sync.Mutex{},
		writeFunc:      writeFunc,
		writing:        0,
		checkQueueChan: make(chan struct{}, 1),
		StopChan:       make(chan *sync.WaitGroup),
	}
}

// StartRWMonitor starts the RWMonitor that manages read and write requests
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

// Clean up the RWMonitor by closing channels and clearing the requests queue
func (m *RWMonitor[T]) CleanUp() {
	// Clear the requests queue

	m.requestsQueue = list.New() // Clear the requests queue

	// Close all channels
	close(m.RequestsChan)
	close(m.checkQueueChan)
	close(m.StopChan)

	// Create new channels to allow restarting the monitor
	m.RequestsChan = make(chan interface{})
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
