package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/buildinfo"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/security"
	"github.com/luckymaomi/llmgateway/internal/store"
	"github.com/luckymaomi/llmgateway/migrations"
)

func main() {
	showVersion := flag.Bool("version", false, "print build identity and exit")
	action := flag.String("action", "status", "database action: status, up, rebuild, rotate-credentials, or recover-administrator")
	confirmDataLoss := flag.Bool("confirm-development-data-loss", false, "confirm rebuilding the configured development or test database")
	confirmKeyRotation := flag.Bool("confirm-key-rotation", false, "confirm re-encrypting Provider credentials with the active master key")
	confirmAccountRecovery := flag.Bool("confirm-account-recovery", false, "confirm administrator password replacement, activation, and session revocation")
	administratorEmail := flag.String("administrator-email", "", "administrator email for offline recovery")
	passwordFile := flag.String("password-file", "", "file containing the replacement administrator password")
	flag.Parse()
	if *showVersion {
		fmt.Fprintln(os.Stdout, buildinfo.JSON())
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	database, err := sql.Open("pgx", cfg.Database.URL)
	if err != nil {
		fatal(fmt.Errorf("open database: %w", err))
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch *action {
	case "status":
		err = migrations.Status(ctx, database)
	case "up":
		err = migrations.Up(ctx, database)
	case "rebuild":
		if err = authorizeRebuild(cfg, *confirmDataLoss); err == nil {
			err = migrations.Reset(ctx, database)
		}
	case "rotate-credentials":
		if !*confirmKeyRotation {
			err = fmt.Errorf("credential rotation requires --confirm-key-rotation")
			break
		}
		var envelope *security.EnvelopeCipher
		envelope, err = security.NewEnvelopeCipher(cfg.Security.ActiveMasterKeyVersion, cfg.Security.MasterKeys)
		if err != nil {
			break
		}
		var result store.CredentialRotationResult
		result, err = store.RotateProviderCredentialEncryption(ctx, database, envelope)
		if err == nil {
			fmt.Printf("credential rotation complete: scanned=%d rotated=%d active_key_version=%d\n", result.Scanned, result.Rotated, result.ActiveKeyVersion)
		}
	case "recover-administrator":
		if !*confirmAccountRecovery {
			err = fmt.Errorf("administrator recovery requires --confirm-account-recovery")
			break
		}
		var password string
		password, err = readPasswordFile(*passwordFile)
		if err != nil {
			break
		}
		var passwordHash string
		passwordHash, err = identity.HashRecoveryPassword(password)
		password = ""
		if err != nil {
			break
		}
		var result store.AdministratorRecoveryResult
		result, err = store.RecoverAdministratorAccess(ctx, database, strings.TrimSpace(*administratorEmail), passwordHash)
		passwordHash = ""
		if err == nil {
			fmt.Printf("administrator access recovered: revoked_sessions=%d\n", result.RevokedSessions)
		}
	default:
		err = fmt.Errorf("unsupported action %q", *action)
	}
	if err != nil {
		fatal(err)
	}
}

func readPasswordFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("password file is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect password file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 2048 {
		return "", fmt.Errorf("password file must be a regular file no larger than 2048 bytes")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	password := strings.TrimSuffix(string(contents), "\n")
	password = strings.TrimSuffix(password, "\r")
	return password, nil
}

func authorizeRebuild(cfg config.Config, confirmed bool) error {
	if cfg.Profile == config.ProfileProduction {
		return fmt.Errorf("database rebuild is disabled in production")
	}
	if !confirmed {
		return fmt.Errorf("rebuild requires --confirm-development-data-loss")
	}
	parsed, err := url.Parse(cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if databaseName == "" || databaseName == "postgres" || strings.HasPrefix(databaseName, "template") {
		return fmt.Errorf("refusing to rebuild unsafe database name %q", databaseName)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
