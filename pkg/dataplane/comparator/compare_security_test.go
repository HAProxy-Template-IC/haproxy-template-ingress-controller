package comparator

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

func TestCommaSeparatedEqual(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected bool
	}{
		{
			name:     "identical strings",
			s1:       "user1,user2,user3",
			s2:       "user1,user2,user3",
			expected: true,
		},
		{
			name:     "same users different order",
			s1:       "user1,user2,user3",
			s2:       "user3,user1,user2",
			expected: true,
		},
		{
			name:     "same users reverse order",
			s1:       "alice,bob,charlie",
			s2:       "charlie,bob,alice",
			expected: true,
		},
		{
			name:     "different users",
			s1:       "user1,user2",
			s2:       "user1,user3",
			expected: false,
		},
		{
			name:     "different count",
			s1:       "user1,user2,user3",
			s2:       "user1,user2",
			expected: false,
		},
		{
			name:     "empty strings",
			s1:       "",
			s2:       "",
			expected: true,
		},
		{
			name:     "single user",
			s1:       "admin",
			s2:       "admin",
			expected: true,
		},
		{
			name:     "single user different",
			s1:       "admin",
			s2:       "user",
			expected: false,
		},
		{
			name:     "duplicates treated as different",
			s1:       "user1,user1,user2",
			s2:       "user1,user2",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := commaSeparatedEqual(tt.s1, tt.s2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGroupEqual(t *testing.T) {
	tests := []struct {
		name     string
		g1       models.Group
		g2       models.Group
		expected bool
	}{
		{
			name: "identical groups",
			g1: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2,user3",
			},
			g2: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2,user3",
			},
			expected: true,
		},
		{
			name: "same users different order - should be equal",
			g1: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2,user3",
			},
			g2: models.Group{
				Name:  "authenticated-users",
				Users: "user3,user1,user2",
			},
			expected: true,
		},
		{
			name: "different names",
			g1: models.Group{
				Name:  "group1",
				Users: "user1,user2",
			},
			g2: models.Group{
				Name:  "group2",
				Users: "user1,user2",
			},
			expected: false,
		},
		{
			name: "different users",
			g1: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2",
			},
			g2: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user3",
			},
			expected: false,
		},
		{
			name: "different user count",
			g1: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2,user3",
			},
			g2: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2",
			},
			expected: false,
		},
		{
			name: "with metadata equal",
			g1: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2",
				Metadata: map[string]interface{}{
					"key1": "value1",
				},
			},
			g2: models.Group{
				Name:  "authenticated-users",
				Users: "user2,user1", // Different order
				Metadata: map[string]interface{}{
					"key1": "value1",
				},
			},
			expected: true,
		},
		{
			name: "with metadata different",
			g1: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2",
				Metadata: map[string]interface{}{
					"key1": "value1",
				},
			},
			g2: models.Group{
				Name:  "authenticated-users",
				Users: "user1,user2",
				Metadata: map[string]interface{}{
					"key1": "value2",
				},
			},
			expected: false,
		},
		{
			name: "empty groups",
			g1: models.Group{
				Name:  "empty-group",
				Users: "",
			},
			g2: models.Group{
				Name:  "empty-group",
				Users: "",
			},
			expected: true,
		},
		{
			name: "nil metadata vs empty map - treated as equal",
			g1: models.Group{
				Name:     "group",
				Users:    "user1",
				Metadata: nil,
			},
			g2: models.Group{
				Name:     "group",
				Users:    "user1",
				Metadata: map[string]interface{}{},
			},
			expected: true, // Both have len 0, semantically equivalent
		},
		{
			name: "both nil metadata",
			g1: models.Group{
				Name:     "group",
				Users:    "user1",
				Metadata: nil,
			},
			g2: models.Group{
				Name:     "group",
				Users:    "user1",
				Metadata: nil,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := groupEqual(tt.g1, tt.g2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGroupsEqualWithIndex(t *testing.T) {
	tests := []struct {
		name     string
		g1       map[string]*models.Group
		g2       map[string]*models.Group
		expected bool
	}{
		{
			name:     "both nil",
			g1:       nil,
			g2:       nil,
			expected: true,
		},
		{
			name:     "both empty",
			g1:       map[string]*models.Group{},
			g2:       map[string]*models.Group{},
			expected: true,
		},
		{
			name: "same groups users in different order",
			g1: map[string]*models.Group{
				"authenticated-users": {
					Name:  "authenticated-users",
					Users: "alice,bob,charlie",
				},
			},
			g2: map[string]*models.Group{
				"authenticated-users": {
					Name:  "authenticated-users",
					Users: "charlie,alice,bob",
				},
			},
			expected: true,
		},
		{
			name: "different groups",
			g1: map[string]*models.Group{
				"group1": {Name: "group1", Users: "user1"},
			},
			g2: map[string]*models.Group{
				"group2": {Name: "group2", Users: "user1"},
			},
			expected: false,
		},
		{
			name: "different group count",
			g1: map[string]*models.Group{
				"group1": {Name: "group1", Users: "user1"},
			},
			g2: map[string]*models.Group{
				"group1": {Name: "group1", Users: "user1"},
				"group2": {Name: "group2", Users: "user2"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := groupsEqualWithIndex(tt.g1, tt.g2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBoolPtrEqual(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		b1       *bool
		b2       *bool
		expected bool
	}{
		{
			name:     "both nil",
			b1:       nil,
			b2:       nil,
			expected: true,
		},
		{
			name:     "first nil second true",
			b1:       nil,
			b2:       &trueVal,
			expected: false,
		},
		{
			name:     "first true second nil",
			b1:       &trueVal,
			b2:       nil,
			expected: false,
		},
		{
			name:     "both true",
			b1:       &trueVal,
			b2:       &trueVal,
			expected: true,
		},
		{
			name:     "both false",
			b1:       &falseVal,
			b2:       &falseVal,
			expected: true,
		},
		{
			name:     "true vs false",
			b1:       &trueVal,
			b2:       &falseVal,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := boolPtrEqual(tt.b1, tt.b2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUserEqual(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		u1       models.User
		u2       models.User
		expected bool
	}{
		{
			name: "identical users",
			u1: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: &trueVal,
				Groups:         "admins,users",
			},
			u2: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: &trueVal,
				Groups:         "admins,users",
			},
			expected: true,
		},
		{
			name: "same groups different order - should be equal",
			u1: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: &trueVal,
				Groups:         "admins,users,operators",
			},
			u2: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: &trueVal,
				Groups:         "operators,admins,users",
			},
			expected: true,
		},
		{
			name: "different username",
			u1: models.User{
				Username: "admin",
				Password: "secret",
			},
			u2: models.User{
				Username: "user",
				Password: "secret",
			},
			expected: false,
		},
		{
			name: "different password",
			u1: models.User{
				Username: "admin",
				Password: "secret1",
			},
			u2: models.User{
				Username: "admin",
				Password: "secret2",
			},
			expected: false,
		},
		{
			name: "different secure password",
			u1: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: &trueVal,
			},
			u2: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: &falseVal,
			},
			expected: false,
		},
		{
			name: "nil vs non-nil secure password",
			u1: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: nil,
			},
			u2: models.User{
				Username:       "admin",
				Password:       "secret",
				SecurePassword: &trueVal,
			},
			expected: false,
		},
		{
			name: "different groups",
			u1: models.User{
				Username: "admin",
				Password: "secret",
				Groups:   "admins",
			},
			u2: models.User{
				Username: "admin",
				Password: "secret",
				Groups:   "users",
			},
			expected: false,
		},
		{
			name: "empty groups",
			u1: models.User{
				Username: "admin",
				Password: "secret",
				Groups:   "",
			},
			u2: models.User{
				Username: "admin",
				Password: "secret",
				Groups:   "",
			},
			expected: true,
		},
		{
			name: "with metadata equal",
			u1: models.User{
				Username: "admin",
				Password: "secret",
				Metadata: map[string]interface{}{"key": "value"},
			},
			u2: models.User{
				Username: "admin",
				Password: "secret",
				Metadata: map[string]interface{}{"key": "value"},
			},
			expected: true,
		},
		{
			name: "with metadata different",
			u1: models.User{
				Username: "admin",
				Password: "secret",
				Metadata: map[string]interface{}{"key": "value1"},
			},
			u2: models.User{
				Username: "admin",
				Password: "secret",
				Metadata: map[string]interface{}{"key": "value2"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := userEqual(tt.u1, tt.u2)
			assert.Equal(t, tt.expected, result)
		})
	}
}
