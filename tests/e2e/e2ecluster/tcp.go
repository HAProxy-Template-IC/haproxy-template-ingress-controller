package e2ecluster

import (
	"fmt"
	"strconv"
	"strings"
)

// HasTCPListener reads a Linux /proc/net/tcp or tcp6 table.
func HasTCPListener(table string, port int) (bool, error) {
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "local_address") {
		return false, fmt.Errorf("TCP socket table has no header")
	}
	listening := false
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return false, fmt.Errorf("TCP socket table has an incomplete row")
		}
		_, encodedPort, found := strings.Cut(fields[1], ":")
		localPort, err := strconv.ParseUint(encodedPort, 16, 16)
		if !found || err != nil {
			return false, fmt.Errorf("TCP socket table has an invalid local port")
		}
		state, err := strconv.ParseUint(fields[3], 16, 8)
		if err != nil {
			return false, fmt.Errorf("TCP socket table has an invalid state")
		}
		if int(localPort) == port && state == 10 {
			listening = true
		}
	}
	return listening, nil
}
