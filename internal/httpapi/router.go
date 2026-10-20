package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/store"
)

type Config struct {
	SecureCookie bool
	WebDir       string
	Logger       *slog.Logger
}

type API struct {
	pool         *pgxpool.Pool
	store        *store.Store
	auth         *auth.Service
	secureCookie bool
	logger       *slog.Logger
}

func NewRouter(pool *pgxpool.Pool, dataStore *store.Store, authService *auth.Service, config Config) http.Handler {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	api := &API{
		pool: pool, store: dataStore, auth: authService,
		secureCookie: config.SecureCookie, logger: logger,
	}

	router := chi.NewRouter()
	router.Use(middleware.Recoverer)
	router.Use(api.securityHeaders)
	router.Get("/healthz", api.health)
	router.Get("/readyz", api.ready)
	router.Get("/auth/nexusdock/start", api.oidcLoginStart)
	router.Get("/auth/nexusdock/callback", api.oidcLoginCallback)

	router.Route("/api/v1", func(r chi.Router) {
		r.Use(requireJSONWrites)
		r.Get("/auth/methods", api.loginMethods)
		r.Post("/auth/login", api.login)
		r.Post("/auth/logout", api.logout)
		r.Group(func(r chi.Router) {
			r.Use(api.requireHuman)
			r.Get("/me", api.me)
			api.mountHumanRoutes(r)
		})
	})

	if config.WebDir != "" {
		router.Handle("/*", spaHandler(config.WebDir))
	}
	return router
}

func (api *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := api.pool.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (api *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
		)
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func spaHandler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." {
			clean = "index.html"
		}
		if !strings.HasPrefix(clean, "..") {
			if info, err := os.Stat(filepath.Join(root, clean)); err == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := os.Open(filepath.Join(root, "index.html"))
		if err != nil {
			if errorsIsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "web assets unavailable", http.StatusInternalServerError)
			return
		}
		defer index.Close()
		info, err := index.Stat()
		if err != nil {
			http.Error(w, "web assets unavailable", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", info.ModTime(), index)
	})
}

func errorsIsNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || err == fs.ErrNotExist)
}
