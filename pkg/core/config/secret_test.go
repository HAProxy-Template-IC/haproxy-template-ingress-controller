package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSecretData_Success(t *testing.T) {
	// Test values: username="admin" (base64: YWRtaW4=), password="password" (base64: cGFzc3dvcmQ=)
	dataRaw := map[string]interface{}{
		"username": "YWRtaW4=",
		"password": "cGFzc3dvcmQ=",
	}

	data, err := ParseSecretData(dataRaw)
	require.NoError(t, err)
	assert.Equal(t, []byte("admin"), data["username"])
	assert.Equal(t, []byte("password"), data["password"])
}

func TestParseSecretData_EmptyMap(t *testing.T) {
	dataRaw := map[string]interface{}{}

	data, err := ParseSecretData(dataRaw)
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestParseSecretData_InvalidBase64(t *testing.T) {
	dataRaw := map[string]interface{}{
		"bad": "not-valid-base64!!!",
	}

	_, err := ParseSecretData(dataRaw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode base64")
	assert.Contains(t, err.Error(), `"bad"`)
}

func TestParseSecretData_InvalidType(t *testing.T) {
	dataRaw := map[string]interface{}{
		"number": 123, // Not a string
	}

	_, err := ParseSecretData(dataRaw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid type")
	assert.Contains(t, err.Error(), `"number"`)
}

func TestParseSecretData_MixedTypes(t *testing.T) {
	dataRaw := map[string]interface{}{
		"valid":   "YWRtaW4=",      // Valid base64
		"invalid": []byte{1, 2, 3}, // Wrong type
	}

	_, err := ParseSecretData(dataRaw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid type")
}

func TestParseSecretData_BinaryData(t *testing.T) {
	// Test that binary data (with null bytes) is handled correctly
	// base64 of []byte{0x00, 0x01, 0x02} = "AAEC"
	dataRaw := map[string]interface{}{
		"binary": "AAEC",
	}

	data, err := ParseSecretData(dataRaw)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x00, 0x01, 0x02}, data["binary"])
}
