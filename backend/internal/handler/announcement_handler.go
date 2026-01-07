package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// PublicAnnouncementHandler handles public announcement endpoints (no auth required)
type PublicAnnouncementHandler struct {
	announcementService *service.AnnouncementService
}

// NewPublicAnnouncementHandler creates a new PublicAnnouncementHandler
func NewPublicAnnouncementHandler(announcementService *service.AnnouncementService) *PublicAnnouncementHandler {
	return &PublicAnnouncementHandler{
		announcementService: announcementService,
	}
}

// PublicAnnouncementResponse represents the API response for a public announcement
type PublicAnnouncementResponse struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Priority  int    `json:"priority"`
	CreatedAt string `json:"created_at"`
}

// GetEnabled returns all enabled announcements
// GET /api/v1/announcements
func (h *PublicAnnouncementHandler) GetEnabled(c *gin.Context) {
	announcements, err := h.announcementService.GetEnabledAnnouncements(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	items := make([]PublicAnnouncementResponse, len(announcements))
	for i, a := range announcements {
		items[i] = PublicAnnouncementResponse{
			ID:        a.ID,
			Title:     a.Title,
			Content:   a.Content,
			Priority:  a.Priority,
			CreatedAt: a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}

	response.Success(c, items)
}
