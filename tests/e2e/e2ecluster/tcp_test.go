//go:build e2e

package e2ecluster

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasTCPListener(t *testing.T) {
	const header = "  sl  local_address rem_address   st\n"
	for _, test := range []struct {
		name    string
		table   string
		want    bool
		invalid bool
	}{
		{name: "listening IPv4", table: header + "0: 00000000:479A 00000000:0000 0A", want: true},
		{name: "listening IPv6", table: header + "0: 00000000000000000000000000000000:479A 00000000000000000000000000000000:0000 0A", want: true},
		{name: "established connection", table: header + "0: 00000000:479A 00000000:0000 01"},
		{name: "old port", table: header + "0: 00000000:479B 00000000:0000 0A"},
		{name: "empty table", table: header},
		{name: "missing table", invalid: true},
		{name: "invalid state", table: header + "0: 00000000:479A 00000000:0000 ZZ", invalid: true},
		{name: "truncated row", table: header + "0: 00000000:479A", invalid: true},
		{name: "invalid port", table: header + "0: 00000000:ZZZZ 00000000:0000 0A", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := HasTCPListener(test.table, 18330)
			if test.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}
