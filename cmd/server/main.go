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

	"proxy-hub/internal/api"
	"proxy-hub/internal/auth"
	"proxy-hub/internal/config"
	"proxy-hub/internal/logbuf"
	"proxy-hub/internal/prober"
	"proxy-hub/internal/store"
	"proxy-hub/internal/tester"
	webassets "proxy-hub/web"
)

func main() {
	env, err := config.LoadEnv()
	if err != nil {
		log.Fatal(err)
	}
	if err := config.ProbeDataDir(env.DataDir); err != nil {
		log.Fatal(err)
	}
	jsonStore, err := store.NewJSONStore(env.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	authService, err := auth.NewService(env.AdminKey, env.DataDir, env.TrustProxyHeaders)
	if err != nil {
		log.Fatal(err)
	}
	logger := logbuf.New(100)
	settings, err := jsonStore.Settings()
	if err != nil {
		log.Fatal(err)
	}
	if err := logger.ConfigureFile(env.DataDir+"/logs/app.log", settings.LogToFile, int64(settings.LogMaxMB)*1024*1024); err != nil {
		log.Fatal(err)
	}
	registry := prober.NewDefaultRegistry()
	proxyTester := tester.New(registry)
	jobManager := tester.NewManager(jsonStore, proxyTester, logger)
	server := api.NewServer(jsonStore, authService, auth.NewLoginLimiter(), proxyTester, jobManager, logger, webassets.FS, env.DataDir, env.TrustProxyHeaders)

	logger.Info("server starting", map[string]string{"listen": env.Listen, "data_dir": env.DataDir})
	httpServer := &http.Server{
		Addr:              env.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- httpServer.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case sig := <-signals:
		logger.Info("server shutting down", map[string]string{"signal": sig.String()})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Fatal(err)
		}
	}
}
