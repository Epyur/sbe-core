package main

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"os"
)

// sendKeyEmail — доставка ключа доступа на email (обёртка над sendMail).
func sendKeyEmail(to, key string) error {
	body := fmt.Sprintf(
		"Ваш ключ доступа к серверу SBE:\n\n%s\n\n"+
			"Ключ привязан к вашему email и устройству, используется один раз при активации и далее хранится в плагине.\n"+
			"Если вы не запрашивали этот ключ, проигнорируйте письмо.\n", key)
	return sendMail(to, "Ключ доступа к серверу SBE", body)
}

// sendMail — универсальная отправка текстового письма (UTF-8) через SMTP.
// Используется для ключей доступа (sendKeyEmail) и обратной связи (/auth/feedback).
func sendMail(to, subject, body string) error {
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "25"
	}
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		from = "noreply@epyur.fvds.ru"
	}
	user := os.Getenv("SMTP_USER")
	pass := os.Getenv("SMTP_PASS")
	skipVerify := os.Getenv("SMTP_SKIP_VERIFY") == "1"

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		from, to, subject, body)

	addr := fmt.Sprintf("%s:%s", host, port)

	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Hello("auth-service"); err != nil {
		return err
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: skipVerify}
		if err := c.StartTLS(tlsConf); err != nil {
			return err
		}
	}

	if user != "" {
		if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
