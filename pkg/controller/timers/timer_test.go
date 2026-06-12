// Copyright 2025 Philipp Hossner
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

package timers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeTimer_Chan_NilWhenInactive(t *testing.T) {
	var st SafeTimer
	assert.Nil(t, st.Chan(), "Chan should return nil when no timer is active")
}

func TestSafeTimer_Chan_NonNilWhenActive(t *testing.T) {
	var st SafeTimer
	st.Reset(time.Hour)
	defer st.Stop()

	assert.NotNil(t, st.Chan(), "Chan should return non-nil when timer is active")
}

func TestSafeTimer_Stop_WhenInactive(t *testing.T) {
	var st SafeTimer
	st.Stop() // should not panic
	assert.Nil(t, st.Chan())
}

func TestSafeTimer_Stop_WhenActive(t *testing.T) {
	var st SafeTimer
	st.Reset(time.Hour)
	require.NotNil(t, st.Chan())

	st.Stop()
	assert.Nil(t, st.Chan())
}

func TestSafeTimer_Stop_AfterFired(t *testing.T) {
	var st SafeTimer
	st.Reset(time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	// Timer has fired but we haven't read the channel
	st.Stop() // should drain and not deadlock
	assert.Nil(t, st.Chan())
}

func TestSafeTimer_Reset_CreatesNewTimer(t *testing.T) {
	var st SafeTimer
	st.Reset(time.Hour)
	defer st.Stop()

	assert.NotNil(t, st.Chan())
}

func TestSafeTimer_Reset_ResetsExistingTimer(t *testing.T) {
	var st SafeTimer
	st.Reset(time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	// Timer has fired, reset should drain and create new
	st.Reset(time.Hour)
	defer st.Stop()

	assert.NotNil(t, st.Chan())
}

func TestSafeTimer_Fired_ClearsReference(t *testing.T) {
	var st SafeTimer
	st.Reset(time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	<-st.Chan()
	st.Fired()

	assert.Nil(t, st.Chan())
}

func TestSafeTimer_Reset_Fires(t *testing.T) {
	var st SafeTimer
	st.Reset(10 * time.Millisecond)

	select {
	case <-st.Chan():
		st.Fired()
	case <-time.After(time.Second):
		t.Fatal("timer did not fire within expected time")
	}

	assert.Nil(t, st.Chan())
}
