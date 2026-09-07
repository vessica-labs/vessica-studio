package main

import (
	"encoding/json"
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/reconcile"
	"io"
	"os"
)

// Reconcile is a pure, offline JSON/base64 checkpoint transform. Cloud calls
// this exact public implementation through the pinned executable.
func cmdReconcile(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: vstd reconcile < input.json > result.json")
	}
	var input reconcile.Input
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 512<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	result, err := reconcile.Merge(input)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
