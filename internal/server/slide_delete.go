package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func (s *Server) handleDeleteSlide(w http.ResponseWriter, r *http.Request) {
	deck, id := r.PathValue("deck"), r.PathValue("id")
	if !studio.ValidDeckName(deck) || !studio.ValidSlideID(id) {
		jsonErr(w, errors.New("invalid deck/slide id"), http.StatusBadRequest)
		return
	}
	err := s.St.DeleteSlide(deck, id, strings.Trim(r.Header.Get("If-Match"), `"`))
	switch {
	case errors.Is(err, studio.ErrLastSlide), errors.Is(err, studio.ErrSlideChanged):
		jsonErr(w, err, http.StatusUnprocessableEntity)
	case err != nil:
		jsonErr(w, err, http.StatusInternalServerError)
	default:
		writeJSON(w, map[string]string{"status": "ok", "id": id})
	}
}

func deleteSlideRoute(method, suffix string) bool {
	id, ok := strings.CutPrefix(suffix, "slide/")
	return method == http.MethodDelete && ok && studio.ValidSlideID(id)
}
