package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AnnouncementHandler handles announcement management endpoints
type AnnouncementHandler struct {
	announcementService *service.AnnouncementService
}

// NewAnnouncementHandler creates a new AnnouncementHandler
func NewAnnouncementHandler(announcementService *service.AnnouncementService) *AnnouncementHandler {
	return &AnnouncementHandler{
		announcementService: announcementService,
	}
}

// AnnouncementResponse represents the API response for an announcement
type AnnouncementResponse struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Enabled   bool   `json:"enabled"`
	Priority  int    `json:"priority"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// List returns paginated announcements
// GET /api/v1/admin/announcements
func (h *AnnouncementHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)

	announcements, total, err := h.announcementService.ListAnnouncements(c.Request.Context(), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	items := make([]AnnouncementResponse, len(announcements))
	for i, a := range announcements {
		items[i] = toAnnouncementResponse(&a)
	}

	response.Paginated(c, items, total, page, pageSize)
}

// GetByID returns a single announcement
// GET /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	announcement, err := h.announcementService.GetAnnouncement(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, toAnnouncementResponse(announcement))
}

// CreateAnnouncementRequest represents the request body for creating an announcement
type CreateAnnouncementRequest struct {
	Title    string `json:"title" binding:"required,max=200"`
	Content  string `json:"content" binding:"required,max=10000"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority" binding:"min=0,max=100"`
}

// Create creates a new announcement
// POST /api/v1/admin/announcements
func (h *AnnouncementHandler) Create(c *gin.Context) {
	var req CreateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	announcement, err := h.announcementService.CreateAnnouncement(c.Request.Context(), &service.CreateAnnouncementInput{
		Title:    req.Title,
		Content:  req.Content,
		Enabled:  req.Enabled,
		Priority: req.Priority,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, toAnnouncementResponse(announcement))
}

// UpdateAnnouncementRequest represents the request body for updating an announcement
type UpdateAnnouncementRequest struct {
	Title    *string `json:"title" binding:"omitempty,max=200"`
	Content  *string `json:"content" binding:"omitempty,max=10000"`
	Enabled  *bool   `json:"enabled"`
	Priority *int    `json:"priority" binding:"omitempty,min=0,max=100"`
}

// Update updates an existing announcement
// PUT /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	var req UpdateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	announcement, err := h.announcementService.UpdateAnnouncement(c.Request.Context(), id, &service.UpdateAnnouncementInput{
		Title:    req.Title,
		Content:  req.Content,
		Enabled:  req.Enabled,
		Priority: req.Priority,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, toAnnouncementResponse(announcement))
}

// Delete deletes an announcement
// DELETE /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	if err := h.announcementService.DeleteAnnouncement(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Announcement deleted successfully"})
}

// toAnnouncementResponse converts service model to API response
func toAnnouncementResponse(a *service.Announcement) AnnouncementResponse {
	return AnnouncementResponse{
		ID:        a.ID,
		Title:     a.Title,
		Content:   a.Content,
		Enabled:   a.Enabled,
		Priority:  a.Priority,
		CreatedAt: a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: a.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
