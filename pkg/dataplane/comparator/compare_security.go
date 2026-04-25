package comparator

import (
	"reflect"
	"slices"
	"strings"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// compareUserlists compares userlist sections between current and desired configurations.
// Uses pointer indexes for zero-copy iteration over users and groups.
func (c *Comparator) compareUserlists(current, desired *parser.StructuredConfig) []Operation {
	getName := func(u *models.Userlist) string { return u.Name }
	currentMap := parserconfig.BuildPointerIndex(current.Userlists, getName)
	desiredMap := parserconfig.BuildPointerIndex(desired.Userlists, getName)

	addedOps := c.findAddedUserlistsWithIndexes(desiredMap, currentMap, desired.UserIndex)
	deletedOps := findDeletedUserlists(currentMap, desiredMap)
	modifiedOps := c.findModifiedUserlistsWithIndexes(currentMap, desiredMap, current.UserIndex, desired.UserIndex, current.GroupIndex, desired.GroupIndex)

	operations := make([]Operation, 0, len(addedOps)+len(deletedOps)+len(modifiedOps))
	operations = append(operations, addedOps...)
	operations = append(operations, deletedOps...)
	operations = append(operations, modifiedOps...)

	return operations
}

// findAddedUserlistsWithIndexes identifies userlist sections that need to be created.
// Uses pointer indexes for zero-copy iteration over users.
func (c *Comparator) findAddedUserlistsWithIndexes(desired, current map[string]*models.Userlist, userIndex map[string]map[string]*models.User) []Operation {
	var operations []Operation
	for name, userlist := range desired {
		if _, exists := current[name]; !exists {
			operations = append(operations, sections.NewUserlistCreate(userlist))
			// Explicitly create each user (Dataplane API may not persist users from request body)
			// Use pointer index for zero-copy iteration
			if users, ok := userIndex[name]; ok {
				for _, user := range users {
					operations = append(operations, sections.NewUserCreate(name, user))
				}
			}
		}
	}
	return operations
}

// findDeletedUserlists identifies userlist sections that need to be removed.
func findDeletedUserlists(current, desired map[string]*models.Userlist) []Operation {
	var operations []Operation
	for name, userlist := range current {
		if _, exists := desired[name]; !exists {
			operations = append(operations, sections.NewUserlistDelete(userlist))
		}
	}
	return operations
}

// findModifiedUserlistsWithIndexes identifies userlist sections that have changed.
// Uses pointer indexes for zero-copy iteration over users and groups.
func (c *Comparator) findModifiedUserlistsWithIndexes(current, desired map[string]*models.Userlist, currentUserIndex, desiredUserIndex map[string]map[string]*models.User, currentGroupIndex, desiredGroupIndex map[string]map[string]*models.Group) []Operation {
	var operations []Operation
	for name, desiredUserlist := range desired {
		currentUserlist, exists := current[name]
		if !exists {
			continue
		}

		if userlistMetadataChangedWithIndexes(name, currentGroupIndex, desiredGroupIndex) {
			// Recreate entire userlist if metadata changed
			operations = append(operations,
				sections.NewUserlistDelete(currentUserlist),
				sections.NewUserlistCreate(desiredUserlist))
		} else {
			// Compare users for fine-grained operations using pointer indexes
			userOps := c.compareUserlistUsersWithIndex(name, currentUserIndex, desiredUserIndex)
			operations = append(operations, userOps...)
		}
	}
	return operations
}

// userlistMetadataChangedWithIndexes checks if userlist metadata (excluding users) has changed.
// Uses pointer indexes for group comparison.
func userlistMetadataChangedWithIndexes(userlistName string, currentGroupIndex, desiredGroupIndex map[string]map[string]*models.Group) bool {
	// Compare groups from indexes
	// If groups are present and different, that requires recreating the userlist
	currentGroups := currentGroupIndex[userlistName]
	desiredGroups := desiredGroupIndex[userlistName]
	return !groupsEqualWithIndex(currentGroups, desiredGroups)
}

// commaSeparatedEqual compares two comma-separated strings order-insensitively.
// Used for Group.Users where order doesn't carry semantic meaning.
func commaSeparatedEqual(s1, s2 string) bool {
	if s1 == s2 {
		return true // Fast path for identical strings
	}

	list1 := strings.Split(s1, ",")
	list2 := strings.Split(s2, ",")

	if len(list1) != len(list2) {
		return false
	}

	slices.Sort(list1)
	slices.Sort(list2)

	for i := range list1 {
		if list1[i] != list2[i] {
			return false
		}
	}
	return true
}

// groupEqual compares two groups with order-insensitive Users comparison.
// This replaces Group.Equal() which does literal string comparison on Users.
func groupEqual(g1, g2 models.Group) bool {
	// Name must match exactly
	if g1.Name != g2.Name {
		return false
	}

	// Users - order-insensitive comparison (usernames are unique, order is irrelevant)
	if !commaSeparatedEqual(g1.Users, g2.Users) {
		return false
	}

	// Metadata - use reflect.DeepEqual like client-native does
	if len(g1.Metadata) != len(g2.Metadata) {
		return false
	}
	for k, v := range g1.Metadata {
		if !reflect.DeepEqual(g2.Metadata[k], v) {
			return false
		}
	}

	return true
}

// boolPtrEqual compares two bool pointers for equality.
func boolPtrEqual(b1, b2 *bool) bool {
	if b1 == nil && b2 == nil {
		return true
	}
	if b1 == nil || b2 == nil {
		return false
	}
	return *b1 == *b2
}

// userEqual compares two users with order-insensitive Groups comparison.
// This replaces User.Equal() which does literal string comparison on Groups.
func userEqual(u1, u2 models.User) bool {
	if u1.Username != u2.Username {
		return false
	}
	if u1.Password != u2.Password {
		return false
	}
	if !boolPtrEqual(u1.SecurePassword, u2.SecurePassword) {
		return false
	}
	// Groups - order-insensitive comparison (group names are unique, order is irrelevant)
	if !commaSeparatedEqual(u1.Groups, u2.Groups) {
		return false
	}
	return reflect.DeepEqual(u1.Metadata, u2.Metadata)
}

// groupsEqualWithIndex compares two group pointer maps for equality.
func groupsEqualWithIndex(g1, g2 map[string]*models.Group) bool {
	if len(g1) != len(g2) {
		return false
	}

	for name, group1 := range g1 {
		group2, exists := g2[name]
		if !exists {
			return false
		}
		if !groupEqual(*group1, *group2) {
			return false
		}
	}

	return true
}

// compareUserlistUsersWithIndex compares users within a userlist using pointer indexes.
func (c *Comparator) compareUserlistUsersWithIndex(userlistName string, currentUserIndex, desiredUserIndex map[string]map[string]*models.User) []Operation {
	return compareNamedMaps(
		currentUserIndex[userlistName], desiredUserIndex[userlistName],
		func(a, b *models.User) bool { return userEqual(*a, *b) },
		func(u *models.User) Operation { return sections.NewUserCreate(userlistName, u) },
		func(u *models.User) Operation { return sections.NewUserDelete(userlistName, u) },
		func(u *models.User) Operation { return sections.NewUserUpdate(userlistName, u) },
	)
}

// compareCrtStores compares crt-store sections between current and desired configurations.
func (c *Comparator) compareCrtStores(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.CrtStores,
		desired.CrtStores,
		func(s *models.CrtStore) string { return s.Name },
		func(a, b *models.CrtStore) bool { return a.Equal(*b) },
		sections.NewCrtStoreCreate,
		sections.NewCrtStoreDelete,
		sections.NewCrtStoreUpdate,
	)
}
