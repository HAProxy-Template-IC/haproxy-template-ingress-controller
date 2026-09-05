// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployplan

import (
	"reflect"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// The exact bodies (Text, Content, Body, Comments) are json:"-", so a baseline
// reconstructed from the agent's /v1/state never carries them. Comparing them
// alone would report every section, file and backend as changed and reload the
// fleet on every reconcile, so each helper falls back to the serialised digest
// whenever either side lacks content.

func sameFileContent(prev, next *renderplan.File) bool {
	if prev == nil || next == nil {
		return false
	}
	if prev.ContentKnown && next.ContentKnown {
		return prev.Content == next.Content
	}
	return prev.Digest != "" && prev.Digest == next.Digest
}

func sameSectionText(prev, next *renderplan.Section) bool {
	if prev == nil || next == nil {
		return false
	}
	if prev.TextKnown && next.TextKnown {
		return prev.Text == next.Text
	}
	return prev.TextDigest != "" && prev.TextDigest == next.TextDigest
}

func sameBackendBody(prev, next *renderplan.Backend) bool {
	if prev.ContentKnown && next.ContentKnown {
		return slices.Equal(prev.Body, next.Body)
	}
	return prev.BodyDigest != "" && prev.BodyDigest == next.BodyDigest
}

func sameBackendComments(prev, next *renderplan.Backend) bool {
	if prev.ContentKnown && next.ContentKnown {
		return slices.Equal(prev.Comments, next.Comments)
	}
	return prev.CommentsDigest == next.CommentsDigest
}

// sameBackendRecord compares the declared record only: RecordDigest excludes
// the body and comments, so the exact branch must clear them too or a
// body-only edit reads as a record change and forces a reload.
func sameBackendRecord(prev, next *renderplan.Backend) bool {
	if prev.ContentKnown && next.ContentKnown {
		left, right := *prev, *next
		left.BodyDigest, right.BodyDigest = "", ""
		left.CommentsDigest, right.CommentsDigest = "", ""
		left.RecordDigest, right.RecordDigest = "", ""
		left.TextDigest, right.TextDigest = "", ""
		left.Body, right.Body = nil, nil
		left.Comments, right.Comments = nil, nil
		return reflect.DeepEqual(left, right)
	}
	return next.RecordDigest != "" && prev.RecordDigest == next.RecordDigest
}
