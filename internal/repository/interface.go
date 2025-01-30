package repository

import (
	"github.com/nickheyer/distroface/internal/models"
)

type Repository interface {
	// USER OPS
	GetUser(username string) (*models.User, error)
	CreateUser(user *models.User) error
	UpdateUser(user *models.User) error
	DeleteUser(username string) error
	ListUsers() ([]*models.User, error)

	// GROUP OPS
	GetGroup(name string) (*models.Group, error)
	CreateGroup(group *models.Group) error
	UpdateGroup(group *models.Group) error
	DeleteGroup(name string) error
	ListGroups() ([]*models.Group, error)

	// ROLE OPS
	GetRole(name string) (*models.Role, error)
	CreateRole(role *models.Role) error
	UpdateRole(role *models.Role) error
	DeleteRole(name string) error
	ListRoles() ([]*models.Role, error)

	// USER GROUP OPS
	AddUserToGroup(username string, groupName string) error
	RemoveUserFromGroup(username string, groupName string) error
	GetUserGroups(username string) ([]string, error)
	UpdateUserGroups(username string, groups []string) error

	// REGISTRY OPS
	ListImageMetadata(owner string) ([]*models.ImageMetadata, error)
	GetImageMetadata(id string) (*models.ImageMetadata, error)
	CreateImageMetadata(metadata *models.ImageMetadata) error
	UpdateImageMetadata(metadata *models.ImageMetadata) error
	DeleteImageTag(repository string, tag string, owner string) error
	DeleteImageMetadata(id string) error
	ListPublicImageMetadata() ([]*models.ImageMetadata, error)
	UpdateImageVisibility(id string, private bool) error
}
