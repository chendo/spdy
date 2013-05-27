package spdy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
)

func NewClientConn(conn net.Conn, client *http.Client, version uint16) (spdyConn Conn, err error) {
	if conn == nil {
		return nil, errors.New("Error: Connection initialised with nil net.conn.")
	}
	if client == nil {
		return nil, errors.New("Error: Connection initialised with nil client.")
	}

	switch version {
	case 3:
		out := new(connV3)
		out.remoteAddr = conn.RemoteAddr().String()
		out.server = nil
		out.client = client
		out.conn = conn
		out.buf = bufio.NewReader(conn)
		if tlsConn, ok := conn.(*tls.Conn); ok {
			out.tlsState = new(tls.ConnectionState)
			*out.tlsState = tlsConn.ConnectionState()
		}
		out.streams = make(map[StreamID]Stream)
		out.output = [8]chan Frame{}
		out.output[0] = make(chan Frame)
		out.output[1] = make(chan Frame)
		out.output[2] = make(chan Frame)
		out.output[3] = make(chan Frame)
		out.output[4] = make(chan Frame)
		out.output[5] = make(chan Frame)
		out.output[6] = make(chan Frame)
		out.output[7] = make(chan Frame)
		out.pings = make(map[uint32]chan<- Ping)
		out.nextPingID = 2
		out.compressor = NewCompressor(3)
		out.decompressor = NewDecompressor(3)
		out.receivedSettings = make(Settings)
		out.lastPushStreamID = 0
		out.lastRequestStreamID = 0
		out.oddity = 1
		out.initialWindowSize = DEFAULT_INITIAL_CLIENT_WINDOW_SIZE
		out.requestStreamLimit = newStreamLimit(NO_STREAM_LIMIT)
		out.pushStreamLimit = newStreamLimit(DEFAULT_STREAM_LIMIT)
		out.pushRequests = make(map[StreamID]*http.Request)
		out.stop = make(chan struct{})

		return out, nil

	default:
		return nil, errors.New("Error: Unrecognised SPDY version.")
	}
}
