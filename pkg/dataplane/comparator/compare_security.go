package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareUserlists compares userlist sections between current and desired configurations.
func (c *Comparator) compareUserlists(current, desired *parser.StructuredConfig) []Operation {
	currentMap := buildUserlistMap(current.Userlists)
	desiredMap := buildUserlistMap(desired.Userlists)

	var operations []Operation
	operations = append(operations, c.findAddedUserlists(desiredMap, currentMap)...)
	operations = append(operations, findDeletedUserlists(currentMap, desiredMap)...)
	operations = append(operations, c.findModifiedUserlists(currentMap, desiredMap)...)

	return operations
}

// buildUserlistMap converts a userlist slice to a map for comparison.
func buildUserlistMap(userlists []*models.Userlist) map[string]*models.Userlist {
	userlistMap := make(map[string]*models.Userlist)
	for i := range userlists {
		userlist := userlists[i]
		if userlist.Name != "" {
			userlistMap[userlist.Name] = userlist
		}
	}
	return userlistMap
}

// findAddedUserlists identifies userlist sections that need to be created.
func (c *Comparator) findAddedUserlists(desired, current map[string]*models.Userlist) []Operation {
	var operations []Operation
	for name, userlist := range desired {
		if _, exists := current[name]; !exists {
			operations = append(operations, sections.NewUserlistCreate(userlist))
			// Explicitly create each user (Dataplane API may not persist users from request body)
			for _, user := range userlist.Users {
				userCopy := user
				operations = append(operations, sections.NewUserCreate(name, &userCopy))
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

// findModifiedUserlists identifies userlist sections that have changed.
func (c *Comparator) findModifiedUserlists(current, desired map[string]*models.Userlist) []Operation {
	var operations []Operation
	for name, desiredUserlist := range desired {
		currentUserlist, exists := current[name]
		if !exists {
			continue
		}

		if userlistMetadataChanged(currentUserlist, desiredUserlist) {
			// Recreate entire userlist if metadata changed
			operations = append(operations,
				sections.NewUserlistDelete(currentUserlist),
				sections.NewUserlistCreate(desiredUserlist))
		} else {
			// Compare users for fine-grained operations
			userOps := c.compareUserlistUsers(name, currentUserlist, desiredUserlist)
			operations = append(operations, userOps...)
		}
	}
	return operations
}

// userlistMetadataChanged checks if userlist metadata (excluding users and groups) has changed.
func userlistMetadataChanged(current, desired *models.Userlist) bool {
	// Compare metadata fields (currently only Name is in UserlistBase)
	// Users and Groups are compared separately for fine-grained operations
	// For now, we only check if there are other structural changes
	// If groups are present and different, that requires recreating the userlist
	if !groupsEqual(current.Groups, desired.Groups) {
		return true
	}

	return false
}

// groupsEqual compares two group maps for equality.
func groupsEqual(g1, g2 map[string]models.Group) bool {
	if len(g1) != len(g2) {
		return false
	}

	for name, group1 := range g1 {
		group2, exists := g2[name]
		if !exists {
			return false
		}
		if !group1.Equal(group2) {
			return false
		}
	}

	return true
}

// compareUserlistUsers compares users within a userlist and generates fine-grained user operations.
func (c *Comparator) compareUserlistUsers(userlistName string, current, desired *models.Userlist) []Operation {
	var operations []Operation

	// Get user maps (they are not pointers in client-native models)
	currentUsers := current.Users
	desiredUsers := desired.Users

	// Find added users
	for username, user := range desiredUsers {
		if _, exists := currentUsers[username]; !exists {
			userCopy := user
			operations = append(operations, sections.NewUserCreate(userlistName, &userCopy))
		}
	}

	// Find deleted users
	for username, user := range currentUsers {
		if _, exists := desiredUsers[username]; !exists {
			userCopy := user
			operations = append(operations, sections.NewUserDelete(userlistName, &userCopy))
		}
	}

	// Find modified users
	for username, desiredUser := range desiredUsers {
		currentUser, exists := currentUsers[username]
		if !exists {
			continue
		}

		// Compare user attributes (password, groups, etc.)
		if !currentUser.Equal(desiredUser) {
			userCopy := desiredUser
			operations = append(operations, sections.NewUserUpdate(userlistName, &userCopy))
		}
	}

	return operations
}

// compareCrtStores compares crt-store sections between current and desired configurations.
func (c *Comparator) compareCrtStores(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.CrtStore)
	for i := range current.CrtStores {
		crtStore := current.CrtStores[i]
		if crtStore.Name != "" {
			currentMap[crtStore.Name] = crtStore
		}
	}

	desiredMap := make(map[string]*models.CrtStore)
	for i := range desired.CrtStores {
		crtStore := desired.CrtStores[i]
		if crtStore.Name != "" {
			desiredMap[crtStore.Name] = crtStore
		}
	}

	// Find added crt-store sections
	for name, crtStore := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			operations = append(operations, sections.NewCrtStoreCreate(crtStore))
		}
	}

	// Find deleted crt-store sections
	for name, crtStore := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewCrtStoreDelete(crtStore))
		}
	}

	// Find modified crt-store sections
	for name, desiredCrtStore := range desiredMap {
		if currentCrtStore, exists := currentMap[name]; exists {
			if !crtStoreEqual(currentCrtStore, desiredCrtStore) {
				operations = append(operations, sections.NewCrtStoreUpdate(desiredCrtStore))
			}
		}
	}

	return operations
}

// crtStoreEqual compares two crt-store sections for equality.
func crtStoreEqual(c1, c2 *models.CrtStore) bool {
	return c1.Equal(*c2)
}
