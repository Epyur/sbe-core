package main

import (
	"context"
	"os"
)

func seedApp(ctx context.Context, s *Server, appID, name, ownerEmail, serviceSecret string) error {
	if appID == "" {
		return nil
	}
	if name == "" {
		name = appID
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO apps (app_id, name, owner_email, service_secret)
VALUES ($1, $2, $3, $4)
ON CONFLICT (app_id) DO UPDATE SET
	name = EXCLUDED.name,
	owner_email = CASE WHEN EXCLUDED.owner_email <> '' THEN EXCLUDED.owner_email ELSE apps.owner_email END,
	service_secret = CASE WHEN EXCLUDED.service_secret <> '' THEN EXCLUDED.service_secret ELSE apps.service_secret END`,
		appID, name, ownerEmail, serviceSecret)
	return err
}

func (s *Server) seedApps(ctx context.Context) error {
	if err := seedApp(ctx, s,
		envOr("MAILER_APP_ID", "mailer"),
		envOr("MAILER_APP_NAME", "Mailer"),
		os.Getenv("MAILER_OWNER_EMAIL"),
		os.Getenv("MAILER_SERVICE_SECRET")); err != nil {
		return err
	}
	if err := seedApp(ctx, s,
		envOr("DOCUMENTS_APP_ID", "documents"),
		envOr("DOCUMENTS_APP_NAME", "Documents"),
		os.Getenv("DOCUMENTS_OWNER_EMAIL"),
		os.Getenv("DOCUMENTS_SERVICE_SECRET")); err != nil {
		return err
	}
	if err := seedApp(ctx, s,
		envOr("EKN_APP_ID", "ekn"),
		envOr("EKN_APP_NAME", "EKN"),
		os.Getenv("EKN_OWNER_EMAIL"),
		os.Getenv("EKN_SERVICE_SECRET")); err != nil {
		return err
	}
	if err := seedApp(ctx, s,
		envOr("LAB_APP_ID", "lab"),
		envOr("LAB_APP_NAME", "Lab"),
		os.Getenv("LAB_OWNER_EMAIL"),
		os.Getenv("LAB_SERVICE_SECRET")); err != nil {
		return err
	}
	if err := seedApp(ctx, s,
		envOr("CONTACTS_APP_ID", "contacts"),
		envOr("CONTACTS_APP_NAME", "Contacts"),
		os.Getenv("CONTACTS_OWNER_EMAIL"),
		os.Getenv("CONTACTS_SERVICE_SECRET")); err != nil {
		return err
	}
	if err := seedApp(ctx, s,
		envOr("PHOTO_APP_ID", "photo"),
		envOr("PHOTO_APP_NAME", "Photobank"),
		os.Getenv("PHOTO_OWNER_EMAIL"),
		os.Getenv("PHOTO_SERVICE_SECRET")); err != nil {
		return err
	}
	return seedApp(ctx, s,
		envOr("AGENT_APP_ID", "agent"),
		envOr("AGENT_APP_NAME", "Agent"),
		os.Getenv("AGENT_OWNER_EMAIL"),
		os.Getenv("AGENT_SERVICE_SECRET"))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
