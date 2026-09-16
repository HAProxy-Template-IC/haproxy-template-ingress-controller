package configpublisher

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestPublishEmptyAuxiliaryContent(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	req := basePublishRequest()
	req.AuxiliaryFiles = &AuxiliaryFiles{
		MapFiles:     []auxiliaryfiles.MapFile{{Path: "/maps/empty.map"}},
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "empty.txt", Path: "/files/empty.txt"}},
		CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "/lists/empty.list"}},
	}
	result, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	api := client.HaproxyTemplateICV1alpha1()
	mapFile, err := api.HAProxyMapFiles(req.TemplateConfigNamespace).Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	generalFile, err := api.HAProxyGeneralFiles(req.TemplateConfigNamespace).Get(ctx, result.GeneralFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	crtList, err := api.HAProxyCRTListFiles(req.TemplateConfigNamespace).Get(ctx, result.CRTListFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	for _, spec := range []any{mapFile.Spec, generalFile.Spec, crtList.Spec} {
		encoded, err := json.Marshal(spec)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(encoded, &fields))
		assert.Equal(t, true, fields["empty"])
		assert.Equal(t, calculateChecksum(""), fields["checksum"])
		assert.NotContains(t, fields, "entries")
		assert.NotContains(t, fields, "content")
		assert.NotContains(t, fields, "compressed")
	}
}
