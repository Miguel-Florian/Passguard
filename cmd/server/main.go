package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embarque la base de fuseaux horaires : l'image Docker n'a pas besoin
	// du paquet tzdata pour que Africa/Douala soit résolu.
	_ "time/tzdata"

	"github.com/entreprise/device-auth/internal/config"
	"github.com/entreprise/device-auth/internal/database"
	"github.com/entreprise/device-auth/internal/handler"
	"github.com/entreprise/device-auth/internal/repository"
	"github.com/entreprise/device-auth/internal/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[passguard] ")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration invalide: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("base de données: %v", err)
	}
	defer pool.Close()
	log.Println("connexion PostgreSQL établie")

	if err := database.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	log.Println("schéma à jour")

	// --- Dépendances --------------------------------------------------
	adminRepo := repository.NewAdminRepo(pool)
	deviceRepo := repository.NewDeviceRepo(pool)
	auditRepo := repository.NewAuditRepo(pool)

	authSvc := service.NewAuthService(adminRepo, cfg)
	scanSvc := service.NewScanService(deviceRepo, auditRepo, cfg)
	importSvc := service.NewImportService(deviceRepo, auditRepo, cfg)
	qrSvc := service.NewQRService(cfg)

	// --- Compte(s) par défaut ----------------------------------------
	if err := authSvc.Bootstrap(ctx); err != nil {
		log.Fatalf("amorçage des comptes: %v", err)
	}
	if cfg.IsProduction() && cfg.AdminPassword == "ChangeMoiEnProduction!2026" {
		log.Println("ATTENTION: le mot de passe administrateur par défaut est toujours actif")
	}

	h := handler.New(cfg, authSvc, scanSvc, importSvc, qrSvc, deviceRepo, auditRepo)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           h.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		log.Printf("serveur démarré sur %s (env=%s, tz=%s)", cfg.BaseURL, cfg.AppEnv, cfg.TimeZone)
		log.Printf("page guérite: %s/scan | administration: %s/admin", cfg.BaseURL, cfg.BaseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serveur: %v", err)
		}
	}()

	// --- Arrêt gracieux -----------------------------------------------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("arrêt en cours...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("arrêt forcé: %v", err)
	}
	log.Println("serveur arrêté proprement")
}
