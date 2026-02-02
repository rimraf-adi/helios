# Detailed Step-by-Step Implementation Plan: 
## Phase 1: Foundation & Architecture Design

### Step 1.1: Understand QUIC's Core Advantages for This Use Case

**Reasoning:**

- QUIC gives you multiplexing (multiple streams) over a single connection without head-of-line blocking
- Built-in encryption (TLS 1.3) - no need to layer security separately
- Connection migration support (switch networks mid-transfer)
- 0-RTT reconnection for resumed transfers
- Better congestion control than TCP

**What you need to know:**

- QUIC uses **streams** (like mini-connections within one connection)
- Each stream is independent - if one stream loses packets, others keep flowing
- You can open hundreds of streams simultaneously
- Streams are lightweight (low overhead per stream)

**Decision point:** How many concurrent streams?

- **Reasoning:** Too few = underutilized bandwidth, too many = overhead
- **Recommendation:** Start with 8-16 streams, make it configurable
- **Why:** Most networks saturate well with 8-16 parallel transfers, more streams = diminishing returns + more memory

### Step 1.2: Design the Protocol Layer

**Why you need this:** Without a clear protocol, sender and receiver won't understand each other. You need structured communication.

**Protocol layers to define:**

**Layer 1: Connection Setup**

```
Purpose: Establish communication, negotiate capabilities
Messages needed:
1. HELLO (client → server)
   - Protocol version
   - Supported features (compression, encryption options)
   - Client capabilities (max streams, buffer size)
   
2. HELLO_ACK (server → client)
   - Accepted version
   - Server capabilities
   - Ready signal

Reasoning: Version negotiation prevents incompatibility as you evolve the protocol
```

**Layer 2: File Metadata Exchange**

```
Purpose: Tell receiver what's coming before sending data

3. FILE_MANIFEST (client → server)
   - List of files to transfer
   - For each file:
     * Full path
     * Size in bytes
     * Hash of entire file (SHA-256 or BLAKE3)
     * Last modified timestamp
     * Number of chunks
     * Chunk size
     
4. TRANSFER_PLAN (server → client)
   - For each file, one of:
     a) NEED_COMPLETE (don't have it, send everything)
     b) HAVE_COMPLETE (already have it, skip)
     c) NEED_PARTIAL (have some chunks, send: [3,7,9,15...])
     
Reasoning: This prevents wasted bandwidth transferring files that already exist
```

**Layer 3: Chunk Transfer**

```
5. CHUNK_HEADER (client → server, per stream)
   - File identifier (which file this chunk belongs to)
   - Chunk index (position in file)
   - Offset in bytes
   - Size of this chunk
   - Hash of this chunk (for verification)
   
6. CHUNK_DATA (follows CHUNK_HEADER)
   - Raw binary data
   - Sent immediately after header on same stream
   
Reasoning: Separate header from data allows receiver to allocate 
buffer and prepare file position before data arrives
```

**Layer 4: Verification & Completion**

```
7. CHUNK_ACK (server → client)
   - Chunk index
   - Status: OK or CORRUPTED
   - If corrupted: hash mismatch details
   
8. FILE_COMPLETE (server → client)
   - File identifier
   - Final verification hash
   - Status
   
Reasoning: Allows sender to know transfer succeeded and can cleanup
```

### Step 1.3: Choose Message Serialization Format

**Options:**

**Option A: Length-Prefixed Binary**

```
[4 bytes: message length][1 byte: message type][N bytes: payload]

Pros:
- Minimal overhead (5 bytes)
- Fast to parse
- Language agnostic

Cons:
- You write custom serialization for each message
- Error-prone (manual byte handling)
```

**Option B: Protocol Buffers (protobuf)**

```
Pros:
- Schema-defined (prevents mistakes)
- Efficient binary format
- Automatic code generation
- Easy to evolve protocol

Cons:
- Dependency on protobuf library
- Slight overhead vs raw binary
```

**Option C: MessagePack / CBOR**

```
Pros:
- JSON-like but binary
- Self-describing
- Good library support

Cons:
- More overhead than protobuf
```

**Recommendation:** Protocol Buffers

**Reasoning:**

- The ~10-20% overhead vs raw binary is negligible compared to data payload
- Schema prevents bugs (type safety)
- Easy to add fields later without breaking compatibility
- Excellent Go library support

### Step 1.4: Design Chunk Strategy

**Key decision: Chunk size**

**Too small (< 1MB):**

- More messages to send/receive
- Higher protocol overhead
- More system calls
- More context switching

**Too large (> 50MB):**

- Higher memory usage (must buffer entire chunk)
- Coarse progress granularity (long time between updates)
- If transfer fails, larger re-send cost
- Harder to parallelize effectively

**Sweet spot: 4-16MB**

**Reasoning:**

- 4MB: Good for slower networks, lower memory usage, fine-grained progress
- 8MB: Balanced - good for most scenarios
- 16MB: Better for very fast networks (10Gbps+), fewer messages

**Your chunk calculation:**

```
File size: 1GB
Chunk size: 8MB

Number of chunks: 1024MB / 8MB = 128 chunks
With 16 streams: ~8 chunks per stream
Transfer time per chunk (at 1Gbps): ~64ms

This means progress updates every ~64ms - good UX
```

**Additional consideration: Variable chunk size**

- Last chunk is usually smaller (file size not perfectly divisible)
- Handle this: `chunkSize = min(CHUNK_SIZE, remainingBytes)`

### Step 1.5: Plan Zero-Copy Strategy

**What is zero-copy and why it matters:**

**Traditional file send (4 copies):**

```
1. Disk → Kernel buffer (DMA)
2. Kernel buffer → Application buffer (CPU copy)
3. Application buffer → Socket buffer (CPU copy)  
4. Socket buffer → Network card (DMA)

CPU touches data twice, wasting cache + CPU cycles
```

**Zero-copy (2 copies):**

```
1. Disk → Kernel buffer (DMA)
2. Kernel buffer → Network card (DMA)

CPU never touches data, just coordinates transfer
```

**Go-specific zero-copy options:**

**Option A: sendfile() syscall**

```go
// Linux-specific
unix.Sendfile(socketFd, fileFd, &offset, count)

Pros:
- True zero-copy
- ~2-3x faster for large files
- Minimal CPU usage

Cons:
- Requires access to raw file descriptor
- QUIC streams in quic-go don't expose underlying socket FD
- Would need to implement custom QUIC or patch quic-go
```

**Option B: io.Copy with WriterTo/ReaderFrom**

```go
// Go's io.Copy detects and uses WriterTo/ReaderFrom interfaces
io.Copy(stream, file)

Pros:
- Works with QUIC streams
- Go runtime may optimize internally
- Clean API

Cons:
- Not true zero-copy (still copies to userspace)
- But reasonably efficient (large internal buffers)
```

**Option C: Manual buffering with large buffers**

```go
buf := make([]byte, 256*1024) // 256KB buffer
for {
    n, _ := file.ReadAt(buf, offset)
    stream.Write(buf[:n])
}

Pros:
- Full control
- Works with any stream
- Can tune buffer size for network

Cons:
- Still copies data
- Need to manage buffers manually
```

**Option D: Memory-mapped I/O (mmap)**

```go
data, _ := unix.Mmap(fileFd, 0, fileSize, unix.PROT_READ, unix.MAP_SHARED)
stream.Write(data[offset:offset+chunkSize])

Pros:
- Kernel manages paging
- Good for random access patterns
- Avoids explicit read() calls

Cons:
- Still copies when writing to stream
- Can exhaust virtual memory on 32-bit systems
- Complexity managing mappings
```

**Recommendation for QUIC in Go:**

**Phase 1 (MVP):** Use Option B (io.Copy)

**Reasoning:**

- Works immediately with quic-go
- Reasonably efficient
- Simple implementation
- Good enough for 90% of use cases

**Phase 2 (Optimization):** Large buffer manual read/write with buffer pool

**Reasoning:**

- Reuse buffers (reduce GC pressure)
- Control buffer size (tune for network)
- ~10-20% improvement over io.Copy

**Phase 3 (Advanced):** Custom QUIC implementation with true zero-copy

**Reasoning:**

- Only if benchmarks show io.Copy is bottleneck
- Requires significant effort
- May involve patching quic-go or using rawconn

## Phase 2: Core Implementation

### Step 2.1: Set Up QUIC Connection (Server Side)

**What you're building:** A server that listens for QUIC connections and accepts multiple streams per connection.

**Sub-steps:**

**2.1.1: Generate TLS Certificate**

```
Why needed: QUIC requires TLS 1.3 (encryption is mandatory)

Options:
A) Self-signed certificate (development/private use)
B) Let's Encrypt certificate (production/internet-facing)

For MVP: Self-signed

Steps:
1. Generate RSA or ECDSA private key
2. Create X.509 certificate with key
3. Configure TLS to use this cert

Reasoning: Self-signed is fine for private networks, 
faster iteration, no external dependencies
```

**2.1.2: Configure QUIC Listener**

```
Key parameters to configure:

1. MaxIncomingStreams
   - How many concurrent streams client can open
   - Set to: NumWorkers * 2 (allows overlap)
   - Example: 16 workers → 32 max streams
   - Reasoning: Some streams closing, some opening, buffer for smooth flow

2. MaxStreamReceiveWindow
   - How much data buffered per stream before backpressure
   - Set to: ChunkSize * 2
   - Example: 8MB chunks → 16MB window
   - Reasoning: Allows one chunk to be processed while next arrives

3. MaxConnectionReceiveWindow
   - Total buffer across all streams
   - Set to: MaxIncomingStreams * MaxStreamReceiveWindow
   - Reasoning: Fair share for all streams

4. KeepAlive
   - Send periodic pings to keep connection alive
   - Set to: 30 seconds
   - Reasoning: Prevents NAT timeout, detects dead connections

5. MaxIdleTimeout
   - Close connection if no activity
   - Set to: 5 minutes
   - Reasoning: Clean up abandoned connections, not too aggressive
```

**2.1.3: Accept Connections Loop**

```
Pattern:
1. listener.Accept() - blocks until client connects
2. Spawn goroutine for each connection
3. Inside goroutine: accept streams, handle file transfer
4. Connection cleanup when done

Reasoning:
- One goroutine per connection scales well
- Go runtime handles scheduling
- Isolates failures (one client crash doesn't affect others)
```

### Step 2.2: Set Up QUIC Connection (Client Side)

**2.2.1: Configure TLS Client**

```
Key decision: Certificate verification

Development: InsecureSkipVerify = true
Reasoning: Faster testing, no cert management

Production: Verify server certificate
- Either: Trust specific cert (pin it)
- Or: Use system cert store
Reasoning: Prevents MITM attacks
```

**2.2.2: Dial Server**

```
Connection parameters:

1. Context with timeout
   - Set to: 30 seconds for initial connection
   - Reasoning: Fail fast if server unreachable

2. Retry logic
   - Exponential backoff: 1s, 2s, 4s, 8s, 16s
   - Max retries: 5
   - Reasoning: Handle temporary network issues gracefully

3. Connection reuse
   - Keep connection open for multiple file transfers
   - Reasoning: Avoid handshake overhead for subsequent files
```

### Step 2.3: Implement File Scanning and Hashing

**Why this step matters:** Before transferring, you need to know:

- What files exist
- Their sizes
- Their content (hash) This allows receiver to skip unchanged files.

**2.3.1: Recursive Directory Walk**

```
Algorithm:
1. Start at root directory
2. For each entry:
   - If file: add to manifest
   - If directory: recurse
3. Collect all file paths

Considerations:
- Skip hidden files? (configurable)
- Follow symlinks? (usually no - avoid loops)
- Max depth? (prevent accidental huge scans)
- Ignore patterns? (.gitignore style)

Implementation pattern:
Use filepath.Walk() - handles recursion, permissions errors
```

**2.3.2: Hash Each File**

```
Hash algorithm choice:

Option A: SHA-256
- Standard, widely used
- Speed: ~200 MB/s (single core)
- 256-bit output (32 bytes)

Option B: BLAKE3
- Modern, very fast
- Speed: ~1000 MB/s (single core)
- Parallelizable (multi-threaded hashing)
- 256-bit output (32 bytes)

Recommendation: BLAKE3
Reasoning:
- 5x faster than SHA-256
- Still cryptographically secure
- Native Go library available
- Better use of CPU during scan phase
```

**2.3.3: Parallel Hashing**

```
Why: Single-threaded hashing wastes CPU on multi-core systems

Pattern:
1. Worker pool with N goroutines (N = num CPUs)
2. Channel of files to hash
3. Each worker:
   - Reads file from channel
   - Hashes it
   - Sends result to output channel
4. Main goroutine collects results

Reasoning:
- Maximizes CPU utilization
- Bounded by disk I/O speed (if HDD)
- Bounded by CPU speed (if SSD)
- On SSD with 8 cores: ~8GB/s hashing throughput
```

**2.3.4: Chunk Hashing (Optional but Recommended)**

```
Should you hash individual chunks?

Pros:
- Detect corruption per-chunk (don't retransmit whole file)
- Enable partial resume (know which chunks are valid)
- Better error isolation

Cons:
- Slower initial scan (hash entire file + each chunk)
- More metadata to transfer

Recommendation: Yes, hash chunks
Reasoning:
- On fast networks, the time cost is minimal
- The reliability benefit is huge
- Enables robust resume capability

Optimization:
- Hash file in streaming fashion
- While streaming, also capture chunk boundaries
- Compute chunk hashes alongside file hash
- Only one pass through file needed
```

### Step 2.4: Implement Manifest Exchange

**Purpose:** Client tells server what files it wants to send. Server responds with what it needs.

**2.4.1: Build File Manifest**

```
Structure:
FileManifest {
    Files: []FileInfo
}

FileInfo {
    RelativePath: string  // "docs/report.pdf"
    Size: int64
    ModTime: int64        // Unix timestamp
    FileHash: [32]byte    // BLAKE3 hash
    NumChunks: uint32
    ChunkHashes: [][32]byte  // Optional: per-chunk hashes
}

Size considerations:
- Per file overhead: ~100 bytes + path length
- For 10,000 files: ~1MB manifest
- With chunk hashes (1GB file, 8MB chunks = 128 chunks): +4KB per file

Reasoning: Overhead is negligible compared to transfer
```

**2.4.2: Send Manifest**

```
Protocol:
1. Client opens control stream (stream 0)
2. Client sends MANIFEST message
3. Client waits for RESPONSE

Serialization:
- Use protobuf (efficient, schema-defined)
- Compress manifest? (only if > 1MB)

Reasoning: Control stream is low-bandwidth, 
separate from data streams
```

**2.4.3: Server Processes Manifest**

```
For each file in manifest:

Check 1: Does file exist locally?
- Yes → Check 2
- No → Mark NEED_COMPLETE

Check 2: Does hash match?
- Yes → Mark SKIP (already have it)
- No → Check 3

Check 3: Is local file bigger/smaller?
- Bigger → Mark NEED_COMPLETE (file shrunk, re-transfer)
- Smaller → Check 4

Check 4: Do we have chunk hashes?
- Yes → Compare chunk hashes, mark NEED_PARTIAL with missing chunks
- No → Mark NEED_COMPLETE (can't verify chunks)

Reasoning: This tree of checks minimizes unnecessary transfers
```

**2.4.4: Send Transfer Plan**

```
Response message:
TransferPlan {
    Files: []FileTransferDecision
}

FileTransferDecision {
    FileIndex: uint32      // Index in original manifest
    Action: enum {
        SKIP,
        NEED_COMPLETE,
        NEED_PARTIAL
    }
    NeededChunks: []uint32 // If NEED_PARTIAL
}

Client receives this and knows exactly what to send

Reasoning: Explicit plan prevents ambiguity, 
client and server agree on what's being transferred
```

### Step 2.5: Implement Parallel Chunk Transfer

**This is the core performance-critical section**

**2.5.1: Worker Pool Design**

```
Architecture:
- Main coordinator goroutine
- N worker goroutines (N = concurrent streams)
- Work queue (channel of chunks to send)
- Progress tracking

Flow:
1. Coordinator: Put all needed chunks in queue
2. Workers: Pull chunk from queue, send it, report completion
3. Coordinator: Track progress, update UI

Why this pattern:
- Dynamic work distribution (fast workers get more chunks)
- Simple synchronization (channels handle locking)
- Easy to add/remove workers
- No explicit thread management
```

**2.5.2: Per-Worker Logic**

```
Worker loop:
for chunk := range workQueue {
    1. Open new QUIC stream
    2. Send CHUNK_HEADER
    3. Send CHUNK_DATA
    4. Wait for ACK (or close stream)
    5. Report completion
    6. Close stream
}

Key decision: Stream lifetime
Option A: One stream per chunk (recommended)
- Clean state management
- Stream close signals chunk completion
- Server can process chunks independently

Option B: Reuse streams
- Less overhead
- More complex state tracking
- Potential head-of-line blocking within stream

Recommendation: Option A
Reasoning: QUIC streams are lightweight, 
clean boundaries, simpler error handling
```

**2.5.3: Reading and Sending Chunks**

```
Efficient chunk sending:

1. Open file once per worker (file descriptor per worker)
   - Reasoning: Avoid file open/close overhead

2. Use ReadAt() for random access
   - Reasoning: Workers read different offsets simultaneously

3. Buffer strategy:
   
   Option A: Allocate per-chunk
   buf := make([]byte, chunkSize)
   - Simple but causes GC pressure
   
   Option B: sync.Pool for buffer reuse
   buf := bufferPool.Get().([]byte)
   defer bufferPool.Put(buf)
   - Reduces GC, reuses memory
   - Recommended
   
4. Write to stream:
   file.ReadAt(buf, offset)
   stream.Write(buf[:actualSize])
   
Optimization: Use io.Copy with LimitReader
   lr := io.LimitReader(io.NewSectionReader(file, offset, size), size)
   io.Copy(stream, lr)
   
Reasoning: Let Go runtime optimize, potentially uses WriteTo/ReadFrom
```

**2.5.4: Handling Backpressure**

```
Problem: If sender sends faster than receiver can process:
- Receiver's memory fills up
- System slows down or crashes

QUIC's solution: Flow control
- Each stream has receive window
- When window fills, sender blocks
- Receiver processes data, window opens again

Your handling:
- Don't try to override QUIC's flow control
- Let stream.Write() block naturally
- This automatically slows sender to receiver's pace

Additional throttling (optional):
- Limit total in-flight chunks
- Example: Max 64 chunks in-flight
- Reasoning: Prevents unbounded memory growth on sender

Implementation:
semaphore := make(chan struct{}, 64)
Before sending chunk: semaphore <- struct{}{}
After ACK received: <-semaphore
```

### Step 2.6: Implement Chunk Reception (Server Side)

**2.6.1: Accept Stream and Read Header**

```
Pattern:
1. Accept incoming stream
2. Read CHUNK_HEADER message
3. Validate header (sane offset, size, file exists)
4. Allocate buffer or prepare write location
5. Read CHUNK_DATA

Error handling:
- If header invalid: close stream with error code
- If file doesn't exist: reject chunk
- If offset out of bounds: reject chunk

Reasoning: Validate early, fail fast, 
prevent writing bad data to disk
```

**2.6.2: Receive Chunk Data**

```
Reading strategy:

Option A: Read into buffer, then write to file
buf := make([]byte, header.Size)
io.ReadFull(stream, buf)
file.WriteAt(buf, header.Offset)

Option B: Stream directly to file
io.CopyN(file, stream, header.Size)

Recommendation: Option A (buffer first)
Reasoning:
- Can verify hash before writing
- Prevents partial writes on corruption
- Allows retry without file corruption

Hash verification:
receivedHash := blake3.Sum256(buf)
if receivedHash != header.Hash {
    // Discard chunk, request retry
    return ErrHashMismatch
}
```

**2.6.3: Writing to File**

```
Concurrent write handling:

Problem: Multiple workers writing to same file
Solution: Each worker writes to different offset (safe)

Go's os.File.WriteAt() is safe for concurrent use:
- Kernel handles synchronization
- Each write is atomic at its offset
- No overlap = no corruption

Pre-allocation (important optimization):
Before receiving any chunks:
file.Truncate(expectedFileSize)

Reasoning:
- Prevents filesystem fragmentation
- Faster writes (no metadata updates)
- Disk space checked upfront (fail early if full)
```

**2.6.4: Progress Tracking**

```
Thread-safe progress counter:

Use atomic operations:
var totalReceived int64
atomic.AddInt64(&totalReceived, int64(chunkSize))

Periodic reporting:
ticker := time.NewTicker(250 * time.Millisecond)
for range ticker.C {
    received := atomic.LoadInt64(&totalReceived)
    percent := float64(received) / float64(totalSize) * 100
    speed := float64(received) / time.Since(start).Seconds()
    // Update UI
}

Reasoning:
- Atomic operations avoid mutex overhead
- 250ms updates = smooth UI without spam
- Speed calculation helps estimate ETA
```

### Step 2.7: Implement Error Handling and Retry Logic

**2.7.1: Network Errors**

```
Types of errors:

1. Connection lost (server crash, network down)
   - Retry entire connection
   - Exponential backoff
   
2. Stream error (individual chunk failed)
   - Retry just that chunk
   - Don't retry if hash keeps failing (corrupted file source)
   
3. Timeout (no response)
   - Set per-chunk timeout: 30 seconds
   - Retry chunk if timeout

Retry strategy:
maxRetries := 3
for attempt := 0; attempt < maxRetries; attempt++ {
    err := sendChunk(chunk)
    if err == nil {
        break
    }
    if isUnrecoverable(err) {
        return err
    }
    time.Sleep(time.Second * (1 << attempt)) // Exponential backoff
}

Reasoning: Transient errors are common (network blips),
but don't retry forever (actual failures need to surface)
```

**2.7.2: Corruption Detection**

```
Hash mismatch handling:

Client side (sending):
1. Hash file chunk
2. Send chunk with hash
3. If receiver reports mismatch:
   - Re-read chunk from disk
   - Re-hash
   - If hash changed → disk corruption (fail)
   - If hash same → network corruption (retry send)

Server side (receiving):
1. Receive chunk
2. Hash received data
3. Compare with header hash
4. If mismatch:
   - Send NACK to client
   - Discard chunk data
   - Wait for retry

Max hash failures: 3
Reasoning: If hash keeps failing, source file is corrupted or malicious
```

**2.7.3: Partial Transfer Resume**

```
Scenario: Transfer interrupted mid-way

Resume strategy:
1. Server tracks which chunks completed successfully
   - Store in memory: map[ChunkIndex]bool
   - Or persist: SQLite database or file

2. On reconnect:
   - Client sends same manifest
   - Server responds with already-received chunks
   - Client only sends missing chunks

3. Chunk-level resume (vs file-level):
   - Benefit: Resume large files efficiently
   - Cost: More complex state management

Implementation:
- Save completed chunks every 10 seconds
- On resume, load saved state
- Mark those chunks as SKIP

Reasoning: For large transfers (100GB+),
resuming is essential (don't waste hours of work)
```

## Phase 3: Performance Optimization

### Step 3.1: Buffer Pool Management

**Why:** Allocating 8MB buffers repeatedly causes GC pressure

**Implementation:**

```go
var bufferPool = sync.Pool{
    New: func() interface{} {
        buf := make([]byte, ChunkSize)
        return &buf
    },
}

Usage:
bufPtr := bufferPool.Get().(*[]byte)
buf := *bufPtr
// ... use buf ...
bufferPool.Put(bufPtr)

Effect: Reduces GC pauses by 30-50%
```

### Step 3.2: Compression (Conditional)

**Should you compress?**

**Analysis:**

- Compressible files (text, logs, source code): 70-90% size reduction
- Incompressible files (video, images, already compressed): 0-5% reduction, wastes CPU

**Decision tree:**

1. Check file extension
2. If likely compressed (jpg, mp4, zip): Don't compress
3. If likely compressible (txt, log, csv): Compress
4. Send compression flag in CHUNK_HEADER

**Algorithm choice:**

- zstd: Fast, good ratio, tunable levels
- Level 3: Fast compression, decent ratio
- Level 9: Slower, better ratio

**Integration point:**

```go
After reading chunk, before sending:
if shouldCompress(file) {
    compressed := zstd.Compress(buf)
    if len(compressed) < len(buf) * 0.9 {  // Only if 10%+ reduction
        buf = compressed
        header.Compressed = true
    }
}
```

### Step 3.3: CPU Profiling and Bottleneck Identification

**Tools:**

```
pprof (Go's built-in profiler)

1. Add profiling endpoint:
   import _ "net/http/pprof"
   go http.ListenAndServe("localhost:6060", nil)

2. During transfer, capture profile:
   go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

3. Analyze:
   top10  // Top 10 CPU consumers
   list functionName  // See source code hot spots

Common bottlenecks:
- Hashing (if using SHA-256, switch to BLAKE3)
- Compression (reduce level or disable)
- Small buffer sizes (increase to 256KB+)
- Lock contention (use atomic operations)
```

### Step 3.4: Network Tuning

**System-level optimizations:**

**Linux:**

```bash
1. Increase socket buffer sizes:
   sysctl -w net.core.rmem_max=134217728  # 128MB
   sysctl -w net.core.wmem_max=134217728

2. Tune TCP (affects QUIC's underlying socket):
   sysctl -w net.ipv4.tcp_rmem="4096 87380 134217728"
   sysctl -w net.ipv4.tcp_wmem="4096 65536 134217728"

3. Enable BBR congestion control (better than Cubic):
   sysctl -w net.ipv4.tcp_congestion_control=bbr
```

**Application-level:**

```go
QUIC config tuning:

InitialStreamReceiveWindow: 512KB → 2MB
- Reasoning: Allows more in-flight data per stream

InitialConnectionReceiveWindow: 1MB → 16MB  
- Reasoning: Supports many concurrent streams

MaxStreamReceiveWindow: 6MB → 16MB
- Reasoning: Matches chunk size, reduces flow control overhead

Effect: 20-40% throughput improvement on high-latency links
```

## Phase 4: TUI (Terminal User Interface)

### Step 4.1: Choose TUI Library

**Options for Go:**

**bubbletea (Recommended)**

- Modern, well-designed
- Elm-inspired architecture (model-update-view)
- Clean separation of concerns
- Active development

**tview**

- Widget-based
- More traditional approach
- Good for complex layouts

**Recommendation: bubbletea**

**Reasoning:**

- Clean architecture makes testing easier
- Composable components
- Great for dynamic updates (progress bars, speed counters)

### Step 4.2: Design TUI Layout

**Screen sections:**

```
┌─────────────────────────────────────────────────────────┐
│ Fast File Transfer v1.0                                 │
├─────────────────────────────────────────────────────────┤
│ Status: Transferring (12/45 files)                      │
│ Speed: 850 MB/s                                         │
│ ETA: 2m 34s                                             │
├─────────────────────────────────────────────────────────┤
│ Current File: large_video.mp4                           │
│ Progress: [████████████████░░░░░░░░░░] 67% (2.1/3.2 GB)│
├─────────────────────────────────────────────────────────┤
│ Overall Progress:                                       │
│ [███████░░░░░░░░░░░░░░░░░░░░░] 27% (12.3/45.8 GB)      │
├─────────────────────────────────────────────────────────┤
│ Active Streams: 14/16                                   │
│ ┌─Stream 1──────────────┐ ┌─Stream 2──────────────┐   │
│ │ Chunk 45 ████░░░ 80%  │ │ Chunk 12 ██████░ 95%  │   │
│ └───────────────────────┘ └───────────────────────┘   │
├─────────────────────────────────────────────────────────┤
│ Recent Events:                                          │
│ • File "document.pdf" completed                         │
│ • Retrying chunk 78 (hash mismatch)                     │
│ • File "archive.zip" started                            │
└─────────────────────────────────────────────────────────┘
```

### Step 4.3: Implement Progress Updates

**Update mechanism:**

**Problem:** Transfer happens in background goroutines, TUI needs updates

**Solution: Channel-based communication**

```go
type ProgressUpdate struct {
    Type: UpdateType  // FILE_STARTED, CHUNK_COMPLETED, etc.
    FileIndex: int
    ChunkIndex: int
    BytesTransferred: int64
}

progressChan := make(chan ProgressUpdate, 100)

Worker goroutines:
    progressChan <- ProgressUpdate{
        Type: CHUNK_COMPLETED,
        ChunkIndex: 42,
        BytesTransferred: 8388608,
    }

TUI goroutine:
    for update := range progressChan {
        model.handleUpdate(update)
        // bubbletea automatically re-renders
    }
```

**Update frequency:**

- Too often (every chunk): UI flickers, high CPU
- Too rare (every 5s): Feels unresponsive
- Sweet spot: Every 100ms

**Aggregation:** Instead of sending every chunk completion, aggregate:

```go
ticker := time.NewTicker(100 * time.Millisecond)
var accumulatedBytes int64

for {
    select {
    case <-ticker.C:
        if accumulatedBytes > 0 {
            progressChan <- ProgressUpdate{BytesTransferred: accumulatedBytes}
            accumulatedBytes = 0
        }
    case chunk := <-completedChunks:
        accumulatedBytes += chunk.Size
    }
}
```

### Step 4.4: Error Display and Logging

**Error levels:**

**1. Transient errors (retrying):**

- Show in "Recent Events"
- Yellow color
- Auto-dismiss after 5 seconds

**2. Recoverable errors (user action needed):**

- Show modal dialog
- "Disk full - free space or cancel transfer?"
- Wait for user input

**3. Fatal errors:**

- Show in red
- Stop transfer
- Display error details
- Offer "View logs" button

**Logging strategy:**

- Log to file: transfer.log
- Include timestamps, chunk IDs, error details
- Rotate logs (max 100MB)
- TUI shows "Press 'L' to view logs"

## Phase 5: Testing Strategy

### Step 5.1: Unit Tests

**What to test:**

**1. Chunking logic:**

```go
Test cases:
- File exactly divisible by chunk size
- File with remainder
- File smaller than chunk size
- Zero-byte file
- Very large file (>4GB, test uint32 overflow)

Assert:
- Correct number of chunks
- Correct offsets
- Correct sizes
- No gaps or overlaps
```

**2. Hash verification:**

```go
Test cases:
- Correct hash
- Modified data (single bit flip)
- Wrong offset
- Truncated data

Assert:
- Detects all modifications
- Accepts only correct data
```

**3. Protocol message encoding/decoding:**

```go
Test cases:
- All message types
- Edge cases (max values, empty strings)
- Backwards compatibility (old client, new server)

Assert:
- Round-trip encoding works
- No data loss
- Handles version mismatches gracefully
```

**4. Buffer pool:**

```go
Test cases:
- Get/Put cycles
- Concurrent access (100 goroutines)
- Memory leak detection

Assert:
- Buffers reused
- No memory leaks
- Thread-safe
```

### Step 5.2: Integration Tests

**5.2.1: Local transfer test:**

```
Setup:
1. Start server on localhost
2. Create test files (various sizes)
3. Run client transfer
4. Verify received files match source

Scenarios:
- Single small file (1 KB)
- Single large file (1 GB)
- Many small files (10,000 × 10KB)
- Mixed sizes
- Nested directories

Assert:
- All files transferred
- Hashes match
- Timestamps preserved
- Directory structure intact
```

**5.2.2: Network simulation:**

```
Use tools like tc (traffic control) to simulate:
- Latency (50ms, 200ms, 500ms)
- Packet loss (1%, 5%, 10%)
- Bandwidth limits (10Mbps, 100Mbps, 1Gbps)
- Network interruption (disconnect for 5 seconds)

Assert:
- Transfer completes successfully
- Performance degradation is acceptable
- Resume works after interruption
```

**5.2.3: Corruption injection:**

```
Modify QUIC stream to randomly corrupt data:
- Flip random bits
- Drop random packets
- Duplicate packets

Assert:
- Corruption detected via hash
- Chunks retried automatically
- Transfer eventually succeeds
- No corrupted data written to disk
```

### Step 5.3: Performance Benchmarks

**5.3.1: Baseline comparison:**

```
Compare against rsync on same hardware:

Test files:
- 10GB of text files (compressible)
- 10GB of video files (incompressible)
- 100,000 small files (1MB each)

Measure:
- Transfer time
- CPU usage
- Memory usage
- Network utilization

Target:
- 2x faster than rsync on LAN
- 5x faster on high-latency WAN
```

**5.3.2: Scalability testing:**

```
Test with varying:
- File sizes: 1KB to 100GB
- Number of files: 1 to 1,000,000
- Concurrent streams: 1 to 64
- Network speeds: 10Mbps to 10Gbps

Measure:
- Throughput vs stream count (find optimal)
- Memory usage vs file count
- CPU usage vs compression settings

Identify bottlenecks and limits
```

**5.3.3: Profiling:**

```
During transfer, collect:
- CPU profile (where time is spent)
- Memory profile (allocations)
- Goroutine profile (concurrency)
- Block profile (where goroutines wait)

Tools:
- pprof (visualize profiles)
- trace (visualize goroutine scheduling)
- perf (Linux system-level profiling)

Optimize hotspots iteratively
```

### Step 5.4: Stress Testing

**5.4.1: Long-running transfers:**

```
Transfer 1TB of data continuously

Monitor:
- Memory leaks (use Valgrind or Go's leak detector)
- File descriptor leaks
- Goroutine leaks
- Performance degradation over time

Run for: 24+ hours
Assert: Stable performance, no crashes
```

**5.4.2: Concurrent transfers:**

```
Run multiple client-server pairs simultaneously

Scenarios:
- 10 clients → 1 server
- 1 client → 10 servers
- 100 clients → 100 servers (different pairs)

Measure:
- Total throughput
- Fairness (each transfer gets fair share)
- Resource limits (max connections OS can handle)
```

**5.4.3: Adversarial testing:**

```
Malicious client scenarios:
- Send invalid protocol messages
- Send corrupted hashes
- Open streams without sending data
- Rapid connect/disconnect
- Exhaust server resources

Assert:
- Server doesn't crash
- Server limits client resource usage
- Invalid requests rejected cleanly
```

## Phase 6: Advanced Features

### Step 6.1: Resume Capability

**6.1.1: State persistence:**

```
Save transfer state to disk:

State file (JSON or protobuf):
{
    "transfer_id": "uuid",
    "files": [
        {
            "path": "file1.bin",
            "size": 1073741824,
            "hash": "abc123...",
            "chunks_completed": [0, 1, 2, 5, 6, ...],
            "chunks_total": 128
        }
    ],
    "timestamp": "2025-01-18T10:30:00Z"
}

Save location: ~/.file-transfer/state/
Save frequency: Every 5 seconds, or every 100 chunks
```

**6.1.2: Resume protocol:**

```
Client reconnects after interruption:

1. Load state file
2. Send RESUME message with transfer_id
3. Server looks up transfer state
4. Server responds with chunks already received
5. Client sends only missing chunks

Optimizations:
- Don't re-hash files (use saved hashes)
- Don't re-send manifest (use saved manifest)
- Resume from exact chunk boundary
```

**6.1.3: Cleanup:**

```
When to delete state:
- After successful transfer completion
- After manual cancellation
- After state file age > 7 days (configurable)

Reasoning: Don't accumulate stale state indefinitely
```

### Step 6.2: Bandwidth Limiting

**6.2.1: Rate limiter implementation:**

```go
Token bucket algorithm:

type RateLimiter struct {
    rate       float64  // bytes per second
    bucket     float64  // current tokens
    maxBucket  float64  // bucket capacity
    lastUpdate time.Time
}

func (rl *RateLimiter) Wait(bytes int64) {
    // Refill bucket based on time elapsed
    now := time.Now()
    elapsed := now.Sub(rl.lastUpdate).Seconds()
    rl.bucket = min(rl.bucket + elapsed*rl.rate, rl.maxBucket)
    rl.lastUpdate = now
    
    // Wait if not enough tokens
    if float64(bytes) > rl.bucket {
        deficit := float64(bytes) - rl.bucket
        waitTime := time.Duration(deficit/rl.rate) * time.Second
        time.Sleep(waitTime)
        rl.bucket = 0
    } else {
        rl.bucket -= float64(bytes)
    }
}
```

**6.2.2: Integration:**

```
Apply rate limiting before sending each chunk:

rateLimiter.Wait(chunkSize)
stream.Write(chunkData)

Reasoning:
- Simple to implement
- Works with existing code
- Accurate enough for most cases

Advanced: Apply at network level using tc (Linux)
```

**6.2.3: Dynamic adjustment:**

```
Allow user to change limit during transfer:

- Listen for key press in TUI
- Update rate limiter parameters
- No need to restart transfer

UI:
Press '+' to increase by 10 MB/s
Press '-' to decrease by 10 MB/s
Press '0' to remove limit
```

### Step 6.3: Encryption (Beyond TLS)

**Note:** QUIC already provides TLS 1.3 encryption

**Additional encryption (if needed):**

**Use case: End-to-end encryption where server shouldn't see data**

**6.3.1: File-level encryption:**

```
Before chunking:
1. Generate random AES-256 key per file
2. Encrypt file using AES-GCM
3. Chunk encrypted file
4. Transfer chunks
5. Send key separately (via different channel or encrypted with recipient's public key)

Receiver:
1. Receive encrypted chunks
2. Assemble file
3. Decrypt using provided key
```

**6.3.2: Chunk-level encryption:**

```
Per chunk:
1. Encrypt chunk with AES-GCM
2. Send encrypted data
3. Include IV (initialization vector) in CHUNK_HEADER

Reasoning: More granular, but more overhead (IV per chunk)
```

**Recommendation:** Only add if explicitly needed

**Reasoning:**

- QUIC's TLS is sufficient for most use cases
- Additional encryption adds complexity
- Performance cost
- Key management burden

### Step 6.4: Multi-path Transfer

**Advanced: Use multiple network interfaces simultaneously**

**6.4.1: Detect available interfaces:**

```go
interfaces, _ := net.Interfaces()
for _, iface := range interfaces {
    addrs, _ := iface.Addrs()
    // Filter for usable addresses (IPv4, not loopback)
    // Create QUIC connection on each interface
}
```

**6.4.2: Load balancing:**

```
Strategy: Round-robin chunks across interfaces

Chunk 1 → Interface A (WiFi)
Chunk 2 → Interface B (Ethernet)
Chunk 3 → Interface C (LTE)
Chunk 4 → Interface A (WiFi)
...

Benefit: Aggregate bandwidth of all interfaces
Example: WiFi (300 Mbps) + Ethernet (1000 Mbps) = 1300 Mbps total
```

**6.4.3: Challenges:**

- IP address management (server needs multiple IPs or NAT traversal)
- Different latencies per interface
- Cost (cellular data might be metered)

**Recommendation:** Advanced feature, implement only if needed

### Step 6.5: Compression Algorithms Comparison

**Test different algorithms on your typical data:**

|Algorithm|Speed (MB/s)|Ratio|CPU %|Use Case|
|---|---|---|---|---|
|None|10000|1.0×|5%|Compressed files, video|
|LZ4|3000|2.0×|20%|Speed-critical, moderate compression|
|Zstd-3|500|2.5×|40%|Balanced (recommended)|
|Zstd-9|100|3.2×|80%|Slow links, compressible data|
|Brotli|50|3.5×|95%|Maximum compression, low priority|

**Adaptive compression:**

```
Measure network speed vs compression speed:

If network < compression:
    Use compression (network is bottleneck)
Else:
    Skip compression (CPU is bottleneck)

Example:
Network: 100 Mbps = 12.5 MB/s
Zstd-3: 500 MB/s
→ Use compression (saves network time)

Network: 10 Gbps = 1250 MB/s
Zstd-3: 500 MB/s
→ Skip compression (wastes CPU time)
```

## Phase 7: Production Readiness

### Step 7.1: Configuration Management

**7.1.1: Config file format:**

```yaml
# config.yaml
network:
  max_streams: 16
  chunk_size_mb: 8
  timeout_seconds: 30
  keep_alive_seconds: 30
  
transfer:
  compression: auto  # auto, always, never
  compression_algorithm: zstd
  compression_level: 3
  bandwidth_limit_mbps: 0  # 0 = unlimited
  
resume:
  enabled: true
  state_dir: ~/.file-transfer/state
  auto_cleanup_days: 7
  
security:
  verify_certificates: true
  cert_file: /path/to/cert.pem
  key_file: /path/to/key.pem
  
logging:
  level: info  # debug, info, warn, error
  file: ~/.file-transfer/transfer.log
  max_size_mb: 100
  rotate: true
```

**7.1.2: Command-line flags override config:**

```bash
file-transfer send \
  --server example.com:4433 \
  --file /path/to/data \
  --streams 32 \
  --compression always \
  --bandwidth-limit 100
```

**7.1.3: Config precedence:**

```
1. Command-line flags (highest priority)
2. Environment variables
3. Config file
4. Built-in defaults (lowest priority)
```

### Step 7.2: Logging and Observability

**7.2.1: Structured logging:**

```go
Use structured logger (e.g., zap, zerolog):

logger.Info("chunk_sent",
    zap.String("file", filename),
    zap.Int("chunk_index", idx),
    zap.Int64("bytes", size),
    zap.Duration("duration", elapsed),
)

Benefits:
- Machine-parseable
- Easy to search/filter
- Can export to monitoring systems
```

**7.2.2: Metrics:**

```
Track and export:
- Total bytes transferred
- Transfer rate (current, average, peak)
- Active connections/streams
- Errors by type
- Chunk retry count
- File completion count

Format: Prometheus metrics or StatsD

Example:
file_transfer_bytes_total{direction="sent"} 1073741824
file_transfer_rate_mbps{direction="sent"} 850.5
file_transfer_errors_total{type="hash_mismatch"} 3
```

**7.2.3: Distributed tracing:**

```
For debugging complex transfers:

Use OpenTelemetry:
- Span per file transfer
- Span per chunk transfer
- Track timing of each operation
- Correlate client and server logs

Helps identify:
- Where time is spent
- Slow chunks
- Bottlenecks in pipeline
```

### Step 7.3: Security Hardening

**7.3.1: Input validation:**

```
Validate all protocol messages:

File paths:
- No absolute paths (prevent writing to /etc)
- No parent directory references (prevent ../ attacks)
- Length limits (prevent memory exhaustion)

Chunk headers:
- Offset + size <= file size
- Size <= MAX_CHUNK_SIZE
- Hash length == expected

Reject invalid messages immediately
```

**7.3.2: Resource limits:**

```
Server-side limits:

Max concurrent connections: 1000
Max streams per connection: 32
Max chunk size: 64 MB
Max file size: 1 TB (configurable)
Max files per manifest: 1,000,000
Max manifest size: 100 MB

Reasoning: Prevent resource exhaustion DoS
```

**7.3.3: Authentication (optional):**

```
If needed, add authentication layer:

Option A: Pre-shared key
- Client sends HMAC of manifest with key
- Server verifies

Option B: Public key authentication
- Client signs manifest with private key
- Server verifies with public key

Option C: OAuth/JWT tokens
- Client obtains token from auth server
- Includes token in HELLO message

Implementation: Add auth field to HELLO message
```

### Step 7.4: Documentation

**7.4.1: User documentation:**

```
README.md:
- Quick start guide
- Installation instructions
- Basic usage examples
- Configuration options
- Troubleshooting

Examples:
# Send a file
file-transfer send --server example.com:4433 --file data.zip

# Receive files (server mode)
file-transfer serve --listen :4433 --output /data

# Resume interrupted transfer
file-transfer resume --transfer-id abc123
```

**7.4.2: Protocol specification:**

```
PROTOCOL.md:
- Message format definitions
- State machine diagrams
- Error codes and meanings
- Version compatibility matrix
- Wire format examples

Reasoning: Allows independent implementations
```

**7.4.3: Architecture documentation:**

```
ARCHITECTURE.md:
- System overview diagram
- Component responsibilities
- Data flow diagrams
- Performance characteristics
- Design decisions and rationale

Reasoning: Helps contributors understand codebase
```

### Step 7.5: Distribution and Deployment

**7.5.1: Binary releases:**

```
Build for multiple platforms:

Linux: amd64, arm64
macOS: amd64, arm64 (M1/M2)
Windows: amd64

Use goreleaser for automated builds:
- Cross-compile
- Create archives
- Generate checksums
- Upload to GitHub releases
```

**7.5.2: Package managers:**

```
Distribute via:
- Homebrew (macOS/Linux): brew install file-transfer
- apt (Debian/Ubuntu): apt install file-transfer
- yum (RHEL/CentOS): yum install file-transfer
- Chocolatey (Windows): choco install file-transfer

Reasoning: Easy installation for users
```

**7.5.3: Docker images:**

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o file-transfer

FROM alpine:latest
COPY --from=builder /app/file-transfer /usr/local/bin/
EXPOSE 4433/udp
ENTRYPOINT ["file-transfer"]
CMD ["serve", "--listen", ":4433"]
```

**7.5.4: Systemd service:**

```ini
[Unit]
Description=File Transfer Server
After=network.target

[Service]
Type=simple
User=filetransfer
ExecStart=/usr/local/bin/file-transfer serve --config /etc/file-transfer/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## Phase 8: Maintenance and Evolution

### Step 8.1: Monitoring in Production

**8.1.1: Health checks:**

```
HTTP endpoint for monitoring:

GET /health
Response:
{
    "status": "healthy",
    "uptime_seconds": 86400,
    "active_connections": 5,
    "total_transfers": 1234,
    "error_rate": 0.02
}

Use for:
- Load balancer health checks
- Monitoring system alerts
- Automatic restarts
```

**8.1.2: Alerting:**

```
Alert conditions:

Critical:
- Server down
- Error rate > 10%
- Disk full

Warning:
- Memory usage > 80%
- CPU usage > 90% for 5 minutes
- Transfer success rate < 95%

Send alerts via:
- Email
- Slack/Discord
- PagerDuty
```

### Step 8.2: Performance Regression Testing

**8.2.1: Benchmark suite:**

```
Run on each release:

1. Standard benchmark files (1MB, 100MB, 1GB, 10GB)
2. Measure throughput on reference hardware
3. Compare to previous version
4. Fail CI if > 10% regression

Store results in database:
- Track performance over time
- Identify when regression introduced
```

**8.2.2: Continuous profiling:**

```
In production (with sampling):
- Collect CPU profiles periodically
- Send to profiling service (e.g., Pyroscope)
- Identify hot paths in real usage
- Optimize based on actual workloads
```

### Step 8.3: Protocol Evolution

**8.3.1: Versioning strategy:**

```
Version in HELLO message:

Client: "I speak version 1, 2, 3"
Server: "I'll use version 3"

Backward compatibility:
- New servers support old clients (for N-1 versions)
- Deprecate old versions with advance notice
- Clear migration path

Feature negotiation:
- Client lists supported features
- Server picks compatible subset
- Allows gradual rollout of features
```

**8.3.2: Feature flags:**

```
Add new features behind flags:

features:
  experimental_multipath: false
  beta_compression_v2: true
  
Allows:
- Testing new features in production
- Gradual rollout
- Quick disable if issues found
```

### Step 8.4: Community and Contributions

**8.4.1: Contribution guidelines:**

```
CONTRIBUTING.md:
- Code style guide (use gofmt, golint)
- Testing requirements (coverage > 80%)
- PR process (review, CI checks)
- License (Apache 2.0, MIT, etc.)
```

**8.4.2: Issue templates:**

```
Bug report template:
- Environment (OS, Go version, app version)
- Steps to reproduce
- Expected vs actual behavior
- Logs and diagnostics

Feature request template:
- Use case description
- Proposed solution
- Alternatives considered
```

### Step 8.5: Long-term Roadmap

**Potential future enhancements:**

1. **P2P mode:** Direct client-to-client transfer without server
2. **Multi-hop routing:** Transfer via intermediate nodes
3. **Deduplication:** Block-level dedup to save bandwidth
4. **Incremental sync:** Like rsync's delta transfer
5. **Cloud integration:** Native S3, GCS, Azure Blob support
6. **GUI:** Desktop application with drag-and-drop
7. **Mobile apps:** iOS and Android clients
8. **Browser support:** WebTransport (QUIC over WebSockets)

## Summary: Implementation Phases

### Phase 1 (Week 1-2): MVP

- ✓ Basic QUIC connection
- ✓ Single file transfer
- ✓ Chunking and hashing
- ✓ Simple CLI

### Phase 2 (Week 3-4): Core Features

- ✓ Parallel streams
- ✓ Progress tracking
- ✓ Error handling and retry
- ✓ Basic TUI

### Phase 3 (Week 5-6): Optimization

- ✓ Buffer pooling
- ✓ Zero-copy (where possible)
- ✓ Compression
- ✓ Performance tuning

### Phase 4 (Week 7-8): Production Ready

- ✓ Resume capability
- ✓ Configuration management
- ✓ Comprehensive testing
- ✓ Documentation

### Phase 5 (Week 9-10): Polish

- ✓ Package for distribution
- ✓ Security hardening
- ✓ Monitoring and observability
- ✓ Community setup

### Phase 6 (Ongoing): Maintenance

- ✓ Bug fixes
- ✓ Performance improvements
- ✓ New features
- ✓ Community support

---

## Key Takeaways

**QUIC advantages:**

- Multiplexing without head-of-line blocking
- Built-in encryption
- Fast connection establishment
- Better for high-latency networks

**Zero-copy challenges:**

- True zero-copy requires raw socket access
- QUIC libraries don't expose this
- Practical approach: Efficient buffering with buffer pools

**Performance factors (in order of impact):**

1. Parallel streams (biggest impact)
2. Chunk size tuning
3. Buffer pooling (reduce GC)
4. Compression (for compressible data)
5. Network tuning (sysctl settings)
6. Zero-copy (marginal in Go with QUIC)

**Critical success factors:**

- Robust error handling and retry
- Good progress visibility (TUI)
- Resume capability for long transfers
- Comprehensive testing
- Clear documentation

This plan gives you a complete roadmap from concept to production-ready file transfer system. Start with Phase 1 MVP, validate it works, then progressively add features. Good luck!