package spdy

import (
  "bytes"
  "fmt"
  "log"
  "net/http"
  "sync"
)

type stream struct {
  sync.RWMutex
  conn           *connection
  streamID       uint32
  requestBody    *bytes.Buffer
  state          StreamState
  input          <-chan Frame
  output         chan<- Frame
  request        *Request
  handler        *ServeMux
  certificates   []Certificate
  headers        Header
  settings       []*Setting
  unidirectional bool
  responseSent   bool
  responseCode   int
  stop           bool
  transferWindow int64
  queuedData     *queue
  wroteHeader    bool
  version        int
}

func (s *stream) Header() Header {
  return s.headers
}

func (s *stream) Ping() <-chan bool {
  return make(chan bool)
}

func (s *stream) Push() (PushWriter, error) {
  return nil, nil
}

func (s *stream) Settings() []*Setting {
  return s.conn.receivedSettings
}

func (s *stream) Write(data []byte) (int, error) {
  if s.stop {
    return 0, ErrCancelled
  }

  if !s.wroteHeader {
    s.WriteHeader(http.StatusOK)
  }

  if len(data) == 0 {
    return 0, nil
  }

  if int64(len(data)) > s.transferWindow {
    s.queuedData.Push(data[s.transferWindow:])
    data = data[:s.transferWindow]
  }

  dataFrame := new(DataFrame)
  dataFrame.StreamID = s.streamID
  dataFrame.Data = data

  s.transferWindow -= int64(len(data))

  s.output <- dataFrame
  if DebugMode {
    fmt.Printf("Debug: Wrote %d bytes of data from stream %d.\n", len(data), s.streamID)
  }

  return len(data), nil
}

func (s *stream) WriteHeader(code int) {
  if s.wroteHeader {
    log.Println("spdy: Error: Multiple calls to ResponseWriter.WriteHeader.")
    return
  }

  s.wroteHeader = true
  s.responseCode = code

  s.headers.Set(":status", fmt.Sprint(code))
  s.headers.Set(":version", "HTTP/1.1")

  synReply := new(SynReplyFrame)
  synReply.Version = uint16(s.version)
  synReply.StreamID = s.streamID
  synReply.Headers = s.headers

  s.output <- synReply
}

func (s *stream) WriteSettings(settings ...*Setting) {
  if settings == nil {
    return
  }

  frame := new(SettingsFrame)
  frame.Version = uint16(s.version)
  frame.Settings = settings
  s.output <- frame
}

func (s *stream) receiveFrame(frame Frame) {
  switch frame := frame.(type) {
  case *DataFrame:
    s.requestBody.Write(frame.Data)

  case *HeadersFrame:
    s.headers.Update(frame.Headers)

  case *WindowUpdateFrame:
    if int64(frame.DeltaWindowSize)+s.transferWindow > 0x80000000 {
      reply := new(RstStreamFrame)
      reply.Version = uint16(s.version)
      reply.StreamID = s.streamID
      reply.StatusCode = RST_STREAM_FLOW_CONTROL_ERROR
      s.output <- reply
      return
    }

    // Grow window and flush queue.
    s.transferWindow += int64(frame.DeltaWindowSize)
    data := s.queuedData.Pop(int(s.transferWindow))
    if data != nil && len(data) > 0 {
      _, err := s.Write(data)
      if err != nil {
        panic(err)
      }
    }
    return

  default:
    panic(fmt.Sprintf("Received unknown frame of type %T.", frame))
  }
}

func (s *stream) wait() {
  s.receiveFrame(<-s.input)
}

func (s *stream) processInput() {
  var frame Frame

  for {
    select {
    case frame = <-s.input:
      s.receiveFrame(frame)

    default:
      return
    }
  }
}

func (s *stream) run() {

  // Make sure Request is prepared.
  s.requestBody = new(bytes.Buffer)
  s.processInput()
  s.request.Body = &readCloserBuffer{s.requestBody}

  // Prime the transfer window to the default 64 kB.
  s.transferWindow = 65536
  s.queuedData = new(queue)

  /***************
   *** HANDLER ***
   ***************/
  s.handler.ServeSPDY(s, s.request)

  if !s.wroteHeader {
    s.headers.Set(":status", "200")
    s.headers.Set(":version", "HTTP/1.1")

    synReply := new(SynReplyFrame)
    synReply.Version = uint16(s.version)
    synReply.Flags = FLAG_FIN
    synReply.StreamID = s.streamID
    synReply.Headers = s.headers

    s.output <- synReply
  } else {
    cancel := new(RstStreamFrame)
    cancel.Version = uint16(s.version)
    cancel.StreamID = s.streamID
    cancel.StatusCode = RST_STREAM_CANCEL

    s.output <- cancel
  }

  s.conn.done.Done()
}

type queue struct {
  data []byte
}

func (q *queue) Push(data []byte) {
  if q.data == nil {
    q.data = data
  } else {
    q.data = append(q.data, data...)
  }
}

func (q *queue) Pop(n int) []byte {
  if n < len(q.data) {
    out := q.data[:n]
    q.data = q.data[n:]
    return out
  }

  out := q.data
  q.data = nil
  return out
}

func (q *queue) Empty() bool {
  return len(q.data) == 0
}

type readCloserBuffer struct {
  *bytes.Buffer
}

func (_ *readCloserBuffer) Close() error {
  return nil
}
