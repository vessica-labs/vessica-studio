package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Platform resources come exclusively from compiled-in engine assets. A host
// must install them from its pinned engine, never trust a tenant's platform label.
func PlatformDeliveryResources() (map[string][]byte, error) {
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		return nil, err
	}
	blocks := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindAllSubmatch(player, -1)
	theme, err := templates.ReadFile("templates/default-theme/theme.css")
	if err != nil {
		return nil, err
	}
	sources := [][]byte{theme}
	for _, block := range blocks {
		sources = append(sources, block[1])
	}
	result := map[string][]byte{}
	for _, source := range sources {
		data, err := deliveryMinifier().Bytes("text/css", source)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(data)
		result["assets/platform/"+hex.EncodeToString(hash[:])+".css"] = data
	}
	return result, nil
}

// ExportPlatformDeliveryResources creates the verified, non-tenant namespace
// independently of any studio. Its filenames are integrity-bound.
func ExportPlatformDeliveryResources(output string) error {
	if output == "" {
		return fmt.Errorf("platform resource output required")
	}
	if err := requireEmptyOrMissing(output); err != nil {
		return err
	}
	resources, err := PlatformDeliveryResources()
	if err != nil {
		return err
	}
	for path, data := range resources {
		if err = writeReleaseFile(output, filepath.Base(path), data); err != nil {
			return err
		}
	}
	return nil
}

func extractPlatformDelivery(root, html string, assets map[string]struct{}) (string, error) {
	resources, err := PlatformDeliveryResources()
	if err != nil {
		return "", err
	}
	// Replace only exact compiled-in CSS, never deck overrides or custom themes.
	player, _ := templates.ReadFile("templates/player.html")
	blocks := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindAllStringSubmatch(string(player), -1)
	theme, _ := templates.ReadFile("templates/default-theme/theme.css")
	replace := func(source, whole string) error {
		data, e := deliveryMinifier().Bytes("text/css", []byte(source))
		if e != nil {
			return e
		}
		hash := sha256.Sum256(data)
		path := "assets/platform/" + hex.EncodeToString(hash[:]) + ".css"
		if _, ok := resources[path]; !ok {
			return fmt.Errorf("unknown platform resource")
		}
		if strings.Contains(html, whole) {
			if e = writeReleaseFile(root, path, data); e != nil {
				return e
			}
			assets[path] = struct{}{}
			html = strings.ReplaceAll(html, whole, `<link rel="stylesheet" href="./`+path+`">`)
		}
		return nil
	}
	for _, b := range blocks {
		if err = replace(b[1], b[0]); err != nil {
			return "", err
		}
	}
	// Only the stock theme prefix is public; deck overrides stay presentation scoped.
	prefix := `<style id="vstd-presentation-styles">` + "\n" + string(theme) + "\n/* deck overrides */"
	if strings.Contains(html, prefix) {
		data, _ := deliveryMinifier().Bytes("text/css", theme)
		hash := sha256.Sum256(data)
		path := "assets/platform/" + hex.EncodeToString(hash[:]) + ".css"
		if err = os.MkdirAll(filepath.Join(root, "assets/platform"), 0o755); err != nil {
			return "", err
		}
		if err = writeReleaseFile(root, path, data); err != nil {
			return "", err
		}
		assets[path] = struct{}{}
		html = strings.ReplaceAll(html, prefix, `<link rel="stylesheet" href="./`+path+`"><style id="vstd-presentation-styles">`)
	}
	return html, nil
}
