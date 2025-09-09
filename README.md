# Read Write Monitor
Repo contains single read write monitor implemented using Stateful Goroutine pattern.

## Requirements
* go ^1.20

## Features
* Queues provided read/write requests in a FIFO manner.
* Manages unsynchronized read/write operations on provided unsafe read/write functions.
* Processes multiple read operations in the same time if they were provided in a row.
* During write operation does not process any other operations.
* Enables stopping it safely ending all ongoing operations, dropping those already queued.
* Returns read/write outcome through channel provided in a read/write request.

## Usage
Place dependency in a go.mod:
```go.mod
dependencies (
    github.com/tomekstefaniak/read-write-monitor v0.1.0
)
```
Create RWMonitor instance providing it unsafe functions and start it by Goroutine:
```go
monitor := NewRWMonitor(unsafeReadFunc, unsafeWriteFunc)
go monitor.StartRWMonitor()
```
Read function must return read result and error while
write function must return error:
```go
readFunc       func() (T, error)    // Function to read data
writeFunc      func(T) error        // Function to write data
```
You can send read/write requests my using RequestsChan:
```go
// Read
resChan := make(chan ReadRes[int], 1)
monitor.RequestsChan <- ReadReq[int]{ResChan: resChan}
result, err := <-resChan // Get output from channel
```
```go
// Write
resChan := make(chan error, 1)
monitor.RequestsChan <- WriteReq[int]{Data: id, ResChan: resChan}
err := <-resChan // Get output from channel
```
Provided channel should be buffered because read/write operations
palce values in channels in a non blocking way, leaving it unchanged when blocked.<br>
Now let's stop the monitor:
```go
var wgStop sync.WaitGroup
wgStop.Add(1)
monitor.StopChan <- &wgStop
wgStop.Wait()
```

## Test
I prepared simple stress test for the Monitor that precisely logs every
read/write's start and end. You can clone repo and rust the test by command:
```bash
go test -race -run TestRWMonitor
```
Unfortunately Go Analyser applied by -race flag gets lost on the CleanUp with still ongoing read/write operations.
That's because it does not understand synchronizing requestsQueue field achieved here:
```go 
// rwmonitor.go
// Wait for all readers and writers to finish
m.readersCountMu.Lock()
readersCount := m.readersCount
m.readersCountMu.Unlock()
writing := atomic.LoadInt32(&m.writing)
if readersCount > 0 || writing > 0 {
    <-m.checkQueueChan
}
```
To exclude false warning of from the -race flag Analyser in the test there is a simple sleep made to make sure every read/write operation has fully ended:
```go
// rwmonitor_test.go
time.Sleep(100 * time.Millisecond)
```

## License
MIT License

## Author
Tomasz Stefaniak
