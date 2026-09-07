package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/vessica-labs/vessica-studio/internal/server"
	"io"
	"os"
	"time"
)

func cmdEditorTransform(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: vstd editor-transform < request.json")
	}
	var in server.EditorTransformInput
	d := json.NewDecoder(io.LimitReader(os.Stdin, 52<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := server.TransformEditor(ctx, in)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
