package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCredentials_Success(t *testing.T) {
	secretData := map[string][]byte{
		"dataplane_username": []byte("admin"),
		"dataplane_password": []byte("adminpass"),
	}

	creds, err := LoadCredentials(secretData)
	require.NoError(t, err)
	require.NotNil(t, creds)

	assert.Equal(t, "admin", creds.DataplaneUsername)
	assert.Equal(t, "adminpass", creds.DataplanePassword)
}

func TestLoadCredentials_NilData(t *testing.T) {
	creds, err := LoadCredentials(nil)
	assert.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "secret data is nil")
}

func TestLoadCredentials_MissingDataplaneUsername(t *testing.T) {
	secretData := map[string][]byte{
		"dataplane_password": []byte("adminpass"),
	}

	creds, err := LoadCredentials(secretData)
	assert.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "dataplane_username")
}

func TestLoadCredentials_MissingDataplanePassword(t *testing.T) {
	secretData := map[string][]byte{
		"dataplane_username": []byte("admin"),
	}

	creds, err := LoadCredentials(secretData)
	assert.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "dataplane_password")
}

func TestLoadCredentials_EmptyValues(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string][]byte
		errField string
	}{
		{
			name: "empty dataplane_username",
			data: map[string][]byte{
				"dataplane_username": []byte(""),
				"dataplane_password": []byte("adminpass"),
			},
			errField: "dataplane_username",
		},
		{
			name: "empty dataplane_password",
			data: map[string][]byte{
				"dataplane_username": []byte("admin"),
				"dataplane_password": []byte(""),
			},
			errField: "dataplane_password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := LoadCredentials(tt.data)
			assert.Error(t, err)
			assert.Nil(t, creds)
			assert.Contains(t, err.Error(), tt.errField)
		})
	}
}
