package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/transport/http/response"
	"github.com/example/auto-anki/internal/transport/httpctx"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	Auth       usecase.AuthService
	Sources    usecase.SourceService
	Generation usecase.GenerationService
	Cards      usecase.CardService
	Export     usecase.ExportService
}

func (h Handler) Routes(mw Authz) http.Handler {
	r := chi.NewRouter()

	r.Post("/api/v1/auth/register", h.register)
	r.Post("/api/v1/auth/login", h.login)

	r.Group(func(pr chi.Router) {
		pr.Use(mw.RequireAuth)

		pr.Post("/api/v1/sources", h.createSource)
		pr.Get("/api/v1/sources", h.listSources)

		pr.Post("/api/v1/jobs/generation", h.createGenJob)
		pr.Get("/api/v1/jobs/generation/{id}", h.getGenJob)

		pr.Get("/api/v1/cards", h.listCards)
		pr.Patch("/api/v1/cards/{id}", h.patchCard)

		pr.Post("/api/v1/exports", h.createExport)
		pr.Get("/api/v1/exports/{id}", h.getExport)
		pr.Get("/api/v1/exports/{id}/download", h.downloadExport)
	})

	// Admin endpoints (in prototype only stubbed via role check middleware)
	r.Group(func(ar chi.Router) {
		ar.Use(func(next http.Handler) http.Handler { return mw.RequireRole(domain.RoleAdmin, next) })
		ar.Get("/api/v1/admin/ping", func(w http.ResponseWriter, r *http.Request) {
			response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
	})

	return r
}

// ---- auth

type registerReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (h Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := usecase.ValidateEmail(req.Email); err != nil {
		response.Err(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := usecase.ValidatePassword(req.Password); err != nil {
		response.Err(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := usecase.ValidateName(req.Name); err != nil {
		response.Err(w, http.StatusBadRequest, err.Error())
		return
	}
	u, token, err := h.Auth.Register(r.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		if errors.Is(err, usecase.ErrEmailTaken) {
			response.Err(w, http.StatusConflict, err.Error())
			return
		}
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	response.JSON(w, http.StatusCreated, map[string]any{
		"user":  map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "role": u.Role},
		"token": token,
	})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "invalid json")
		return
	}
	u, token, err := h.Auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		response.Err(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"user":  map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "role": u.Role},
		"token": token,
	})
}

// ---- sources

func (h Handler) createSource(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpctx.UserID(r.Context())
	if !ok {
		response.Err(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		response.Err(w, http.StatusBadRequest, "multipart expected")
		return
	}
	title := r.FormValue("title")
	typ := domain.SourceType(r.FormValue("type"))
	lang := r.FormValue("language")
	var langPtr *string
	if lang != "" {
		langPtr = &lang
	}

	file, hdr, err := r.FormFile("file")
	if err != nil {
		response.Err(w, http.StatusBadRequest, "file required")
		return
	}
	defer file.Close()

	if title == "" {
		title = hdr.Filename
	}
	if !isValidSourceType(typ) {
		response.Err(w, http.StatusBadRequest, "invalid type")
		return
	}

	src, err := h.Sources.CreateAndEnqueue(r.Context(), usecase.CreateSourceInput{
		UserID:           userID,
		Title:            title,
		Type:             typ,
		OriginalFilename: hdr.Filename,
		Language:         langPtr,
		Body:             limitReader(file, 50<<20), // 50MB prototype limit
	})
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	response.JSON(w, http.StatusCreated, src)
}

func (h Handler) listSources(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpctx.UserID(r.Context())
	if !ok {
		response.Err(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	srcs, err := h.Sources.List(r.Context(), userID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	response.JSON(w, http.StatusOK, srcs)
}

// ---- generation jobs

type createGenReq struct {
	SourceID       string   `json:"source_id"`
	CardSetID      *string  `json:"card_set_id"`
	CardType       string   `json:"card_type"`
	MaxCards       int      `json:"max_cards"`
	TargetLanguage *string  `json:"target_language"`
	ModelName      *string  `json:"model_name"`
	Temperature    *float64 `json:"temperature"`
	TopK           *int     `json:"top_k"`
	Seed           *int     `json:"seed"`
}

func (h Handler) createGenJob(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpctx.UserID(r.Context())
	var req createGenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "invalid json")
		return
	}
	srcID, err := uuid.Parse(req.SourceID)
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid source_id")
		return
	}
	var csID *uuid.UUID
	if req.CardSetID != nil {
		tmp, err := uuid.Parse(*req.CardSetID)
		if err != nil {
			response.Err(w, http.StatusBadRequest, "invalid card_set_id")
			return
		}
		csID = &tmp
	}
	cardType := domain.CardType(req.CardType)
	if cardType == "" {
		cardType = domain.CardTypeMixed
	}
	if req.MaxCards <= 0 || req.MaxCards > 200 {
		req.MaxCards = 30
	}

	job, err := h.Generation.CreateAndEnqueue(r.Context(), usecase.CreateGenerationJobInput{
		UserID:         userID,
		SourceID:       srcID,
		CardSetID:      csID,
		CardType:       cardType,
		MaxCards:       req.MaxCards,
		TargetLanguage: req.TargetLanguage,
		ModelName:      req.ModelName,
		Temperature:    req.Temperature,
		TopK:           req.TopK,
		Seed:           req.Seed,
	})
	if err != nil {
		if errors.Is(err, usecase.ErrSourceNotIndexed) {
			response.Err(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, usecase.ErrForbidden) {
			response.Err(w, http.StatusForbidden, err.Error())
			return
		}
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}

	response.JSON(w, http.StatusCreated, job)
}

func (h Handler) getGenJob(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpctx.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid id")
		return
	}
	job, err := h.Generation.Get(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, usecase.ErrForbidden) {
			response.Err(w, http.StatusForbidden, "forbidden")
			return
		}
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	response.JSON(w, http.StatusOK, job)
}

// ---- cards

func (h Handler) listCards(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpctx.UserID(r.Context())

	var jobID *uuid.UUID
	if v := r.URL.Query().Get("job_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			response.Err(w, http.StatusBadRequest, "invalid job_id")
			return
		}
		jobID = &id
	}
	var setID *uuid.UUID
	if v := r.URL.Query().Get("card_set_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			response.Err(w, http.StatusBadRequest, "invalid card_set_id")
			return
		}
		setID = &id
	}
	var st *domain.CardStatus
	if v := r.URL.Query().Get("status"); v != "" {
		tmp := domain.CardStatus(v)
		st = &tmp
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	cards, err := h.Cards.List(r.Context(), userID, jobID, setID, st, limit, offset)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	response.JSON(w, http.StatusOK, cards)
}

type patchCardReq struct {
	Status   *string `json:"status"`
	Question *string `json:"question"`
	Answer   *string `json:"answer"`
	Extra    *string `json:"extra"`
}

func (h Handler) patchCard(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpctx.UserID(r.Context())
	cardID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req patchCardReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "invalid json")
		return
	}
	var st *domain.CardStatus
	if req.Status != nil {
		tmp := domain.CardStatus(*req.Status)
		st = &tmp
	}

	ok, err := h.Cards.Patch(r.Context(), userID, cardID, usecase.CardPatch{
		Status:   st,
		Question: req.Question,
		Answer:   req.Answer,
		Extra:    req.Extra,
	})
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	if !ok {
		response.Err(w, http.StatusNotFound, "not found")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- export

type createExportReq struct {
	CardSetID string `json:"card_set_id"`
	Format    string `json:"format"` // csv|apkg
}

func (h Handler) createExport(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpctx.UserID(r.Context())
	var req createExportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "invalid json")
		return
	}
	setID, err := uuid.Parse(req.CardSetID)
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid card_set_id")
		return
	}
	format := domain.ExportFormat(req.Format)
	if format == "" {
		format = domain.ExportCSV
	}
	j, err := h.Export.CreateAndRun(r.Context(), usecase.CreateExportInput{
		UserID:    userID,
		CardSetID: setID,
		Format:    format,
	})
	if err != nil {
		response.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, j)
}

func (h Handler) getExport(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpctx.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid id")
		return
	}
	j, err := h.Export.Get(r.Context(), userID, id)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	response.JSON(w, http.StatusOK, j)
}

func (h Handler) downloadExport(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpctx.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid id")
		return
	}
	j, err := h.Export.Get(r.Context(), userID, id)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "server error")
		return
	}
	if j.Status != domain.JobReady || j.FilePath == nil {
		response.Err(w, http.StatusConflict, "export not ready")
		return
	}
	http.ServeFile(w, r, *j.FilePath)
}

func isValidSourceType(t domain.SourceType) bool {
	switch t {
	case domain.SourcePDF, domain.SourceDOCX, domain.SourceHTML, domain.SourceMarkdown, domain.SourceText:
		return true
	default:
		return false
	}
}

func limitReader(r multipart.File, n int64) io.Reader {
	return io.LimitReader(r, n)
}

// Authz abstracts middleware functions used in routing (удобно для тестов).
type Authz interface {
	RequireAuth(http.Handler) http.Handler
	RequireRole(domain.Role, http.Handler) http.Handler
}
