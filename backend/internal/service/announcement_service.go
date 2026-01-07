package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/announcement"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrAnnouncementNotFound = infraerrors.NotFound("ANNOUNCEMENT_NOT_FOUND", "announcement not found")
)

// Announcement represents the service layer announcement model
type Announcement struct {
	ID        int64
	Title     string
	Content   string
	Enabled   bool
	Priority  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateAnnouncementInput represents input for creating an announcement
type CreateAnnouncementInput struct {
	Title    string
	Content  string
	Enabled  bool
	Priority int
}

// UpdateAnnouncementInput represents input for updating an announcement
type UpdateAnnouncementInput struct {
	Title    *string
	Content  *string
	Enabled  *bool
	Priority *int
}

// AnnouncementService handles announcement operations
type AnnouncementService struct {
	client *ent.Client
}

// NewAnnouncementService creates a new AnnouncementService
func NewAnnouncementService(client *ent.Client) *AnnouncementService {
	return &AnnouncementService{
		client: client,
	}
}

// ListAnnouncements returns paginated announcements for admin
func (s *AnnouncementService) ListAnnouncements(ctx context.Context, page, pageSize int) ([]Announcement, int64, error) {
	offset := (page - 1) * pageSize

	// Count total
	total, err := s.client.Announcement.Query().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count announcements: %w", err)
	}

	// Query with pagination
	entities, err := s.client.Announcement.Query().
		Order(ent.Desc(announcement.FieldPriority), ent.Desc(announcement.FieldCreatedAt)).
		Offset(offset).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list announcements: %w", err)
	}

	result := make([]Announcement, len(entities))
	for i, e := range entities {
		result[i] = s.toServiceModel(e)
	}

	return result, int64(total), nil
}

// GetAnnouncement returns a single announcement by ID
func (s *AnnouncementService) GetAnnouncement(ctx context.Context, id int64) (*Announcement, error) {
	entity, err := s.client.Announcement.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrAnnouncementNotFound
		}
		return nil, fmt.Errorf("get announcement: %w", err)
	}

	result := s.toServiceModel(entity)
	return &result, nil
}

// CreateAnnouncement creates a new announcement
func (s *AnnouncementService) CreateAnnouncement(ctx context.Context, input *CreateAnnouncementInput) (*Announcement, error) {
	entity, err := s.client.Announcement.Create().
		SetTitle(input.Title).
		SetContent(input.Content).
		SetEnabled(input.Enabled).
		SetPriority(input.Priority).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create announcement: %w", err)
	}

	result := s.toServiceModel(entity)
	return &result, nil
}

// UpdateAnnouncement updates an existing announcement
func (s *AnnouncementService) UpdateAnnouncement(ctx context.Context, id int64, input *UpdateAnnouncementInput) (*Announcement, error) {
	update := s.client.Announcement.UpdateOneID(id)

	if input.Title != nil {
		update.SetTitle(*input.Title)
	}
	if input.Content != nil {
		update.SetContent(*input.Content)
	}
	if input.Enabled != nil {
		update.SetEnabled(*input.Enabled)
	}
	if input.Priority != nil {
		update.SetPriority(*input.Priority)
	}

	entity, err := update.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrAnnouncementNotFound
		}
		return nil, fmt.Errorf("update announcement: %w", err)
	}

	result := s.toServiceModel(entity)
	return &result, nil
}

// DeleteAnnouncement deletes an announcement
func (s *AnnouncementService) DeleteAnnouncement(ctx context.Context, id int64) error {
	err := s.client.Announcement.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrAnnouncementNotFound
		}
		return fmt.Errorf("delete announcement: %w", err)
	}
	return nil
}

// GetEnabledAnnouncements returns all enabled announcements for public display
func (s *AnnouncementService) GetEnabledAnnouncements(ctx context.Context) ([]Announcement, error) {
	entities, err := s.client.Announcement.Query().
		Where(announcement.EnabledEQ(true)).
		Order(ent.Desc(announcement.FieldPriority), ent.Desc(announcement.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("get enabled announcements: %w", err)
	}

	result := make([]Announcement, len(entities))
	for i, e := range entities {
		result[i] = s.toServiceModel(e)
	}

	return result, nil
}

// toServiceModel converts ent entity to service model
func (s *AnnouncementService) toServiceModel(e *ent.Announcement) Announcement {
	return Announcement{
		ID:        e.ID,
		Title:     e.Title,
		Content:   e.Content,
		Enabled:   e.Enabled,
		Priority:  e.Priority,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}
