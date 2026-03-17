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
place values in channels in a non blocking way, leaving it unchanged when blocked.<br>
Now let's stop the monitor:
```go
var wgStop sync.WaitGroup
wgStop.Add(1)
monitor.StopChan <- &wgStop
wgStop.Wait()
```

## Test
I prepared simple stress test for the Monitor that precisely logs every
read/write's start and end. You can clone repo and run the test by command:
```bash
go test -race -run TestRWMonitor
```
The test uses `time.Sleep(100ms)` before stopping the monitor to avoid a false positive from the race detector.
The root cause: `operationsWG.Done()` is called via `defer` inside the user-provided read/write functions —
before the internal `read()`/`write()` goroutine finishes its bookkeeping (decrementing `readersCount`, sending the signal).
So `operationsWG.Wait()` only guarantees the user functions completed, not that the goroutines fully exited.
The sleep gives those goroutines time to finish, ensuring `CleanUp` does not race with them on channel operations.

## License
MIT License

## Author
Tomasz Stefaniak
