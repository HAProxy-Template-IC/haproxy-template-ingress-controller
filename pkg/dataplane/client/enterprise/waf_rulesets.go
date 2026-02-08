package enterprise

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// GetAllRulesets retrieves all WAF rulesets.
func (w *WAFOperations) GetAllRulesets(ctx context.Context) ([]WafRuleset, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetWafRulesets(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetWafRulesets(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetWafRulesets(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF rulesets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF rulesets failed with status %d", resp.StatusCode)
	}

	var result []WafRuleset
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF rulesets response: %w", err)
	}
	return result, nil
}

// GetRuleset retrieves a specific WAF ruleset by name.
func (w *WAFOperations) GetRuleset(ctx context.Context, name string) (*WafRuleset, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetWafRuleset(ctx, name)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetWafRuleset(ctx, name)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetWafRuleset(ctx, name)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF ruleset '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF ruleset '%s' failed with status %d", name, resp.StatusCode)
	}

	var result WafRuleset
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF ruleset response: %w", err)
	}
	return &result, nil
}

// CreateRuleset creates a new WAF ruleset from a file.
func (w *WAFOperations) CreateRuleset(ctx context.Context, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateWafRulesetWithBody(ctx, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CreateWafRulesetWithBody(ctx, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.CreateWafRulesetWithBody(ctx, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF ruleset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF ruleset failed with status %d", resp.StatusCode)
	}
	return nil
}

// ReplaceRuleset replaces a WAF ruleset.
func (w *WAFOperations) ReplaceRuleset(ctx context.Context, name string, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceWafRulesetWithBody(ctx, name, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceWafRulesetWithBody(ctx, name, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.ReplaceWafRulesetWithBody(ctx, name, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF ruleset '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF ruleset '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// DeleteRuleset deletes a WAF ruleset.
func (w *WAFOperations) DeleteRuleset(ctx context.Context, name string) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteWafRuleset(ctx, name)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteWafRuleset(ctx, name)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.DeleteWafRuleset(ctx, name)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF ruleset '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF ruleset '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// GetAllRulesetFiles retrieves all files in a WAF ruleset.
func (w *WAFOperations) GetAllRulesetFiles(ctx context.Context, rulesetName, subDir string) ([]string, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetWafFilesParams{SubDir: &subDir}
			return c.GetWafFiles(ctx, rulesetName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetWafFilesParams{SubDir: &subDir}
			return c.GetWafFiles(ctx, rulesetName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetWafFilesParams{SubDir: &subDir}
			return c.GetWafFiles(ctx, rulesetName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF files for ruleset '%s': %w", rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF files for ruleset '%s' failed with status %d", rulesetName, resp.StatusCode)
	}

	var result []string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF files response: %w", err)
	}
	return result, nil
}

// GetRulesetFile retrieves a specific file from a WAF ruleset.
func (w *WAFOperations) GetRulesetFile(ctx context.Context, rulesetName, fileName, subDir string) ([]byte, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetWafFileParams{SubDir: &subDir}
			return c.GetWafFile(ctx, rulesetName, fileName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetWafFileParams{SubDir: &subDir}
			return c.GetWafFile(ctx, rulesetName, fileName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetWafFileParams{SubDir: &subDir}
			return c.GetWafFile(ctx, rulesetName, fileName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF file '%s' from ruleset '%s': %w", fileName, rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF file '%s' from ruleset '%s' failed with status %d", fileName, rulesetName, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// CreateRulesetFile creates a new file in a WAF ruleset.
func (w *WAFOperations) CreateRulesetFile(ctx context.Context, rulesetName, subDir string, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.CreateWafFileParams{SubDir: &subDir}
			return c.CreateWafFileWithBody(ctx, rulesetName, params, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.CreateWafFileParams{SubDir: &subDir}
			return c.CreateWafFileWithBody(ctx, rulesetName, params, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.CreateWafFileParams{SubDir: &subDir}
			return c.CreateWafFileWithBody(ctx, rulesetName, params, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF file in ruleset '%s': %w", rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF file in ruleset '%s' failed with status %d", rulesetName, resp.StatusCode)
	}
	return nil
}

// ReplaceRulesetFile replaces a file in a WAF ruleset.
func (w *WAFOperations) ReplaceRulesetFile(ctx context.Context, rulesetName, fileName, subDir string, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.ReplaceWafFileParams{SubDir: &subDir}
			return c.ReplaceWafFileWithBody(ctx, rulesetName, fileName, params, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.ReplaceWafFileParams{SubDir: &subDir}
			return c.ReplaceWafFileWithBody(ctx, rulesetName, fileName, params, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.ReplaceWafFileParams{SubDir: &subDir}
			return c.ReplaceWafFileWithBody(ctx, rulesetName, fileName, params, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF file '%s' in ruleset '%s': %w", fileName, rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF file '%s' in ruleset '%s' failed with status %d", fileName, rulesetName, resp.StatusCode)
	}
	return nil
}

// DeleteRulesetFile deletes a file from a WAF ruleset.
func (w *WAFOperations) DeleteRulesetFile(ctx context.Context, rulesetName, fileName, subDir string) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteWafFileParams{SubDir: &subDir}
			return c.DeleteWafFile(ctx, rulesetName, fileName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteWafFileParams{SubDir: &subDir}
			return c.DeleteWafFile(ctx, rulesetName, fileName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteWafFileParams{SubDir: &subDir}
			return c.DeleteWafFile(ctx, rulesetName, fileName, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF file '%s' from ruleset '%s': %w", fileName, rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF file '%s' from ruleset '%s' failed with status %d", fileName, rulesetName, resp.StatusCode)
	}
	return nil
}
