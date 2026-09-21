//go:build integration

package integration

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/streaming/pkg/httpstream"
)

type forwardConnection struct {
	httpstream.Connection
	closed chan bool
	once   sync.Once
}

func (c *forwardConnection) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *forwardConnection) CloseChan() <-chan bool { return c.closed }

type forwardDialer func() (httpstream.Connection, string, error)

func (d forwardDialer) Dial(...string) (httpstream.Connection, string, error) { return d() }

func TestAgentForwardOwnsAdvertisedPort(t *testing.T) {
	h := &HAProxyInstance{AgentPort: 5555}
	connection := &forwardConnection{closed: make(chan bool)}
	var competing net.Listener
	dialer := forwardDialer(func() (httpstream.Connection, string, error) {
		var err error
		// Another test takes a released candidate before the forwarder binds it.
		competing, err = net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", h.LocalPort))
		return connection, portforward.PortForwardProtocolV1Name, err
	})
	t.Cleanup(func() {
		h.stopAgentForward()
		if competing != nil {
			_ = competing.Close()
		}
	})
	require.NoError(t, h.startAgentForward(dialer))
	require.NotEqual(t, competing.Addr().(*net.TCPAddr).Port, int(h.LocalPort),
		"a ready tunnel must not advertise another test's IPv4 listener")
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", h.LocalPort))
	if listener != nil {
		_ = listener.Close()
	}
	require.Error(t, err, "the advertised IPv4 port must remain reserved by the forwarder")
	require.Equal(t, fmt.Sprintf("http://127.0.0.1:%d", h.LocalPort), h.AgentURL())
	h.stopAgentForward()
	listener, err = net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", h.LocalPort))
	require.NoError(t, err, "cleanup must release the forwarded port before returning")
	require.NoError(t, listener.Close())
}

func TestAgentForwardReportsDialFailure(t *testing.T) {
	h := &HAProxyInstance{AgentPort: 5555}
	cause := errors.New("test connection refused")
	dialer := forwardDialer(func() (httpstream.Connection, string, error) {
		return nil, "", cause
	})
	require.ErrorContains(t, h.startAgentForward(dialer), cause.Error())
	require.Nil(t, h.stopChan)
	require.Nil(t, h.forwardDone)
}
