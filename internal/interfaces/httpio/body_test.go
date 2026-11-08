package httpio

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application/api"
)

func TestJSONReadDeadlineAndConnectionReuse(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[h2], func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
				defer cancel()
				_, err := ReadJSONBody(w, r.WithContext(ctx), 64)
				if err != nil {
					w.WriteHeader(api.Classify(err).Status)
					return
				}
				_, _ = io.WriteString(w, "ok")
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			client.Timeout = 2 * time.Second
			request, err := http.NewRequest("POST", server.URL, bytes.NewBufferString("{}"))
			require.NoError(t, err)
			response, err := client.Do(request)
			require.NoError(t, err)
			require.Equal(t, 200, response.StatusCode)
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			require.NoError(t, err)
			require.Equal(t, "ok", string(data))
			require.Equal(t, map[bool]int{false: 1, true: 2}[h2], response.ProtoMajor)
			// A completed body must clear the connection read deadline, including on HTTP/2.
			time.Sleep(80 * time.Millisecond)
			var reused atomic.Bool
			request, err = http.NewRequest("POST", server.URL, bytes.NewBufferString("{}"))
			require.NoError(t, err)
			request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) }}))
			response, err = client.Do(request)
			require.NoError(t, err)
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			require.Equal(t, 200, response.StatusCode)
			require.True(t, reused.Load())

			// A stalled request on either protocol must finish without waiting for the sender.
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			request, err = http.NewRequest("POST", server.URL, reader)
			require.NoError(t, err)
			request.ContentLength = 32
			written := make(chan struct{})
			go func() { defer close(written); _, _ = writer.Write([]byte("{")) }()
			response, err = client.Do(request)
			require.NoError(t, err)
			require.Equal(t, 504, response.StatusCode)
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			writer.Close()
			<-written
		})
	}
}
