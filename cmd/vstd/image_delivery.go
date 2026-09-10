package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"os"
)

func cmdImageDelivery(args []string) error {
	fs := flag.NewFlagSet("image-delivery", flag.ContinueOnError)
	input := fs.String("input", "", "source PNG, JPEG, or WebP")
	output := fs.String("output", "", "new display WebP file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" || *output == "" || fs.NArg() != 0 {
		return fmt.Errorf("image-delivery requires --input FILE --output FILE")
	}
	result, err := studio.BuildImageDelivery(*input, *output)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
