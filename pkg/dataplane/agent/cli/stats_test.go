// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const showStatFrontends = "# pxname,svname,qcur,conn_rate,conn_rate_max,conn_tot,\n" +
	"status,FRONTEND,,3,10,4711,\n" +
	"http-tcp,FRONTEND,,12,40,1200,\n" +
	"https,FRONTEND,,1,4,30,\n" +
	"http_frontend,FRONTEND,,12,40,,\n"

func TestParseFrontendConnectionsSumsTrafficFrontends(t *testing.T) {
	total, err := parseFrontendConnections(showStatFrontends, map[string]bool{"status": true})
	require.NoError(t, err)
	assert.Equal(t, uint64(1230), total, "status is ignored and an empty counter counts as zero")
}

func TestParseFrontendConnectionsLocatesTheColumnByHeader(t *testing.T) {
	shifted := "# pxname,svname,conn_tot,extra\nhttp-tcp,FRONTEND,7,x\nback,BACKEND,9,x\n"
	total, err := parseFrontendConnections(shifted, nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), total)
}

func TestParseFrontendConnectionsRejectsAMissingHeader(t *testing.T) {
	_, err := parseFrontendConnections("http-tcp,FRONTEND,1\n", nil)
	require.Error(t, err)
	_, err = parseFrontendConnections("# pxname,svname,qcur\nhttp-tcp,FRONTEND,1\n", nil)
	require.Error(t, err)
}
