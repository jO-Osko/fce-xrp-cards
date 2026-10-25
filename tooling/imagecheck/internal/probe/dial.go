package probe

import (
	"net"
	"time"
)

// Dial reports, per address, whether a TCP connection to it succeeds within timeout.
func Dial(addrs []string, timeout time.Duration) map[string]bool {
	open := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		c, err := net.DialTimeout("tcp", a, timeout)
		if err == nil {
			_ = c.Close()
		}
		open[a] = err == nil
	}
	return open
}
