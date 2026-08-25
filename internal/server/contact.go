package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"
)

const contactWindow = time.Hour
const contactLimit = 5

var resendSendURL = "https://api.resend.com/emails"

type contactRequest struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Organization string `json:"organization"`
	InquiryType  string `json:"inquiry_type"`
	Message      string `json:"message"`
	Website      string `json:"website"` // honeypot; real visitors never fill this
}

func (s *Server) handleContact(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key, from, to := os.Getenv("RESEND_API_KEY"), os.Getenv("RESEND_FROM"), os.Getenv("VSTD_CONTACT_TO")
	if key == "" || from == "" || to == "" {
		jsonErr(w, fmt.Errorf("contact form is not configured"), http.StatusServiceUnavailable)
		return
	}

	var req contactRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		jsonErr(w, fmt.Errorf("invalid contact form"), http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	req.Organization = strings.TrimSpace(req.Organization)
	req.InquiryType = strings.TrimSpace(req.InquiryType)
	req.Message = strings.TrimSpace(req.Message)

	// Bots commonly complete hidden fields. Return success without generating
	// mail so they cannot use the response to tune around the trap.
	if strings.TrimSpace(req.Website) != "" {
		writeJSON(w, map[string]string{"status": "sent"})
		return
	}
	if len(req.Name) < 2 || len(req.Name) > 120 || len(req.Organization) > 160 ||
		len(req.InquiryType) < 2 || len(req.InquiryType) > 120 || len(req.Message) < 10 || len(req.Message) > 4000 {
		jsonErr(w, fmt.Errorf("please complete the required fields"), http.StatusBadRequest)
		return
	}
	parsed, err := mail.ParseAddress(req.Email)
	if err != nil || !strings.EqualFold(parsed.Address, req.Email) || len(req.Email) > 254 {
		jsonErr(w, fmt.Errorf("please enter a valid email address"), http.StatusBadRequest)
		return
	}
	if !s.allowContact(contactClient(r), time.Now()) {
		jsonErr(w, fmt.Errorf("too many inquiries; please try again later"), http.StatusTooManyRequests)
		return
	}

	org := req.Organization
	if org == "" {
		org = "Not provided"
	}
	text := fmt.Sprintf("New inquiry from mattkropp.vessica.ai\n\nName: %s\nEmail: %s\nOrganization: %s\nInquiry: %s\n\nMessage:\n%s\n",
		req.Name, req.Email, org, req.InquiryType, req.Message)
	payload := map[string]any{
		"from":     from,
		"to":       []string{to},
		"reply_to": req.Email,
		"subject":  "Speaking inquiry from " + req.Name,
		"text":     text,
	}
	body, code, err := postJSON(resendSendURL, key, payload)
	if err != nil {
		jsonErr(w, fmt.Errorf("unable to send inquiry"), http.StatusBadGateway)
		return
	}
	if code >= 300 {
		jsonErr(w, fmt.Errorf("unable to send inquiry: %s", trim(body, 160)), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"status": "sent"})
}

func contactClient(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return forwarded
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) allowContact(client string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contactMints == nil {
		s.contactMints = make(map[string][]time.Time)
	}
	cutoff := now.Add(-contactWindow)
	recent := s.contactMints[client][:0]
	for _, sent := range s.contactMints[client] {
		if sent.After(cutoff) {
			recent = append(recent, sent)
		}
	}
	if len(recent) >= contactLimit {
		s.contactMints[client] = recent
		return false
	}
	s.contactMints[client] = append(recent, now)
	return true
}
