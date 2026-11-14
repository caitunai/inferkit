package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/viper"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/logger"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/api/route"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/ocr"
	"github.com/caitun/inferkit/services/pulse"
	"github.com/caitun/inferkit/services/reclassify"
	"github.com/caitun/inferkit/xutil"
)

// Server represents server data.
type Server struct {
	ImageFilter           *imagefilter.Service
	OCR                   *ocr.Service
	ImageRecords          *db.ImageStore
	ImageReclassification *reclassify.Service
	Pulse                 *pulse.Client
	Port                  string
	Mode                  string
	CollectImageResults   bool
}

const (
	noHTTPTimeout          time.Duration = 0
	defaultShutdownTimeout               = 5 * time.Second
	httpServerTaskName                   = "http_server"
)

// NewServer creates a new server.
func NewServer(port, mode string) *Server {
	return &Server{Port: port, Mode: mode}
}

// Start starts the service lifecycle.
func (s *Server) Start(ctx context.Context) {
	defer s.closeImageReclassification()
	r := s.newRouter() //nolint:contextcheck // Router setup only registers callbacks; their contexts come from incoming HTTP requests, not the server lifecycle.
	srv := &http.Server{
		Addr:              ":" + s.Port,
		Handler:           r,
		ReadHeaderTimeout: configuredDuration("server.readHeaderTimeout", noHTTPTimeout),
		ReadTimeout:       configuredDuration("server.readTimeout", noHTTPTimeout),
		WriteTimeout:      configuredDuration("server.writeTimeout", noHTTPTimeout),
		IdleTimeout:       configuredDuration("server.idleTimeout", noHTTPTimeout),
	}

	startServerTask(ctx, func(context.Context) {
		serve(srv)
	})

	shutdownContext, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-shutdownContext.Done()
	s.ImageReclassification.Stop()

	log.Info().Msg("Shutting down server...")
	shutdownTimeout := configuredDuration("server.shutdownTimeout", defaultShutdownTimeout)
	shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownContext); err != nil {
		log.Error().Err(err).Msg("Server forced to shutdown")
		return
	}

	log.Info().Msg("Server exiting")
}

func (s *Server) closeImageReclassification() {
	if err := s.ImageReclassification.Close(); err != nil {
		log.Error().Err(err).Msg("close image reclassification worker")
	}
}

func startServerTask(ctx context.Context, task xutil.Task) {
	xutil.Go(ctx, httpServerTaskName, task)
}

func (s *Server) newRouter() *gin.Engine {
	if s.Mode == gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(requestid.New())
	r.Use(logger.SetLogger(logger.WithLogger(func(c *gin.Context, _ zerolog.Logger) zerolog.Logger {
		tml := log.Logger.With()
		traceID := c.GetHeader("x-trace-id")
		if traceID != "" {
			tml = tml.Str("traceID", traceID)
		}
		spanID := c.GetHeader("x-span-id")
		if spanID != "" {
			tml = tml.Str("spanID", spanID)
		}
		logID := c.GetHeader("x-log-id")
		if logID != "" {
			tml = tml.Str("logID", logID)
		}
		tml = tml.Str("requestID", requestid.Get(c))
		tmp := tml.Logger()
		c.Request = c.Request.WithContext(tmp.WithContext(c.Request.Context()))
		return tmp.With().Str("namespace", "ginRequest").Logger()
	})))
	r.Use(gin.Recovery())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     configuredOrigins(),
		AllowMethods:     []string{"POST", "PUT", "PATCH", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "x-requested-with"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		AllowOriginFunc: func(origin string) bool {
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return strings.Contains(u.Host, "localhost") || strings.Contains(u.Host, "127.0.0.1")
		},
		MaxAge: 12 * time.Hour,
	}))
	route.InitRoute(base.NewRouter(r), s.ImageFilter, s.ImageRecords, s.CollectImageResults, s.OCR, s.Pulse, s.ImageReclassification)
	r.HTMLRender = base.NewRender()
	return r
}

func configuredOrigins() []string {
	origins := []string{"https://www.example.com", "https://example.com"}
	if origin := viper.GetString("url"); origin != "" {
		origins = append(origins, origin)
	}
	return origins
}

func serve(srv *http.Server) {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error().Err(err).Msg("listen error")
	}
}

func configuredDuration(key string, fallback time.Duration) time.Duration {
	value := viper.GetDuration(key)
	if value <= 0 {
		return fallback
	}
	return value
}
