package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	pushservice "momo-poo/internal/push"
	"momo-poo/internal/ratelimit"
	"momo-poo/internal/store"
)

type config struct {
	listenAddr   string
	databasePath string
	location     *time.Location
	writeLimit   int
	readLimit    int
	vapidSubject string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.databasePath), 0o755); err != nil {
		return errors.New("create database directory: " + err.Error())
	}

	db, err := store.Open(context.Background(), store.Config{Path: cfg.databasePath})
	if err != nil {
		return err
	}
	defer db.Close()
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return errors.New("generate VAPID keys: " + err.Error())
	}
	privateKey, publicKey, err = db.EnsureVAPIDKeys(context.Background(), privateKey, publicKey)
	if err != nil {
		return err
	}
	push := pushservice.New(db, publicKey, privateKey, cfg.vapidSubject)

	app := newApp(db, cfg.location,
		ratelimit.NewWrite(cfg.writeLimit, cfg.location, 4096),
		ratelimit.NewRead(cfg.readLimit, 4096),
	)
	app.push = push
	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	shutdownSignals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Momo Poo listening on %s (%s)", cfg.listenAddr, cfg.location)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-shutdownSignals.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return err
		}
	}
	return nil
}

func loadConfig() (config, error) {
	locationName := envOr("APP_TIMEZONE", "Local")
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return config{}, errors.New("load APP_TIMEZONE: " + err.Error())
	}
	writeLimit, err := positiveEnvInt("WRITE_LIMIT_PER_DAY", 50)
	if err != nil {
		return config{}, err
	}
	readLimit, err := positiveEnvInt("READ_LIMIT_PER_MINUTE", 60)
	if err != nil {
		return config{}, err
	}
	vapidSubject := envOr("VAPID_SUBJECT", "https://github.com/leejayhsu/momo-poo")
	if !validVAPIDSubject(vapidSubject) {
		return config{}, errors.New("VAPID_SUBJECT must be a public HTTPS URL or mailto address")
	}
	return config{
		listenAddr:   envOr("LISTEN_ADDR", ":8080"),
		databasePath: envOr("DATABASE_PATH", "./data/momo-poo.db"),
		location:     location,
		writeLimit:   writeLimit,
		readLimit:    readLimit,
		vapidSubject: vapidSubject,
	}, nil
}

func validVAPIDSubject(value string) bool {
	subject, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch subject.Scheme {
	case "https":
		return subject.Hostname() != "" && subject.Hostname() != "localhost"
	case "mailto":
		at := strings.LastIndexByte(subject.Opaque, '@')
		return at > 0 && strings.Contains(subject.Opaque[at+1:], ".")
	default:
		return false
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func positiveEnvInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return n, nil
}
