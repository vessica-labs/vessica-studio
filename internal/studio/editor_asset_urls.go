package studio

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ValidateEditorAssetURLs admits only HTTPS delivery metadata. URLs are never
// written to source; the save path restores the original logical names.
func ValidateEditorAssetURLs(urls map[string]string, keepalive string) error {
	if len(urls) > 10000 {
		return fmt.Errorf("too many editor asset URLs")
	}
	for path, destination := range urls {
		if !strings.HasPrefix(path, "/library/") && !strings.HasPrefix(path, "/assets/video/") {
			return fmt.Errorf("invalid editor asset path")
		}
		if !validReleasePath(strings.TrimPrefix(path, "/")) {
			return fmt.Errorf("invalid editor asset path")
		}
		if err := validateDeliveryURL(destination); err != nil {
			return err
		}
	}
	if keepalive != "" {
		return validateDeliveryURL(keepalive)
	}
	return nil
}
func validateDeliveryURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || len(value) > 4096 {
		return fmt.Errorf("invalid editor delivery URL")
	}
	return nil
}
func RestoreEditorAssetPaths(body []byte, urls map[string]string) []byte {
	value := string(body)
	// JSON and plain HTML bodies both use URL-safe destinations.
	keys := make([]string, 0, len(urls))
	for path := range urls {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for _, path := range keys {
		value = strings.ReplaceAll(value, urls[path], path)
	}
	return []byte(value)
}
func ApplyEditorAssetURLs(html string, urls map[string]string, keepalive string) (string, error) {
	if err := ValidateEditorAssetURLs(urls, keepalive); err != nil {
		return "", err
	}
	if len(urls) == 0 && keepalive == "" {
		return html, nil
	}
	keys := make([]string, 0, len(urls))
	for path := range urls {
		keys = append(keys, path)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	for _, path := range keys {
		html = strings.ReplaceAll(html, path, urls[path])
	}
	data, _ := json.Marshal(urls)
	ping, _ := json.Marshal(keepalive)
	script := `<script>window.VSTDAssetURLs=` + string(data) + `;window.VSTDAssetURL=url=>window.VSTDAssetURLs[url]||url;(function(){const url=` + string(ping) + `;if(url)setInterval(()=>{const image=new Image();image.src=url+'?t='+Date.now();},120000);})();</script>`
	html = strings.Replace(html, "<script>", script+"<script>", 1)
	return html, nil
}
