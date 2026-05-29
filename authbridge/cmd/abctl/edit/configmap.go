// Package edit implements abctl's in-place pipeline editor. The flow is:
// fetch the agent's ConfigMap via kubectl, locate the pipeline: subtree,
// open just that subtree in the user's $EDITOR, splice the edit back into
// the original ConfigMap manifest, kubectl apply --server-side, then poll
// /reload/status until the framework reloads.
//
// All kubectl interaction goes through the Runner injection seam so tests
// can stub it out.
package edit

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// FindPipelineRange returns the byte offsets [start, end) in innerYAML
// that span the "pipeline:" subtree, including the "pipeline:" key line
// itself but not any following top-level keys. Used by the editor to
// extract just the pipeline subtree for the user, and by Splice to
// replace it with the user's edit.
//
// Returns an error if innerYAML is not valid YAML or if no top-level
// "pipeline" key exists.
func FindPipelineRange(innerYAML []byte) (start, end int, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal(innerYAML, &root); err != nil {
		return 0, 0, fmt.Errorf("parse runtime YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return 0, 0, fmt.Errorf("runtime YAML is not a document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return 0, 0, fmt.Errorf("runtime YAML root is not a mapping")
	}

	// Children of a MappingNode alternate key, value, key, value, ...
	// Find the index of the "pipeline" key, capture its line, and find
	// the next sibling's line (or end-of-document if it's the last key).
	pipelineKeyIdx := -1
	for i := 0; i < len(doc.Content); i += 2 {
		k := doc.Content[i]
		if k.Value == "pipeline" {
			pipelineKeyIdx = i
			break
		}
	}
	if pipelineKeyIdx == -1 {
		return 0, 0, fmt.Errorf("no top-level pipeline key in runtime YAML")
	}

	pipelineKeyLine := doc.Content[pipelineKeyIdx].Line // 1-indexed
	var nextKeyLine int                                  // 1-indexed; 0 if pipeline is last
	if pipelineKeyIdx+2 < len(doc.Content) {
		nextKeyLine = doc.Content[pipelineKeyIdx+2].Line
	}

	// Map line numbers to byte offsets. yaml.v3 Line is 1-indexed.
	lineStarts := []int{0} // lineStarts[i] = byte offset where line i+1 starts
	for i, b := range innerYAML {
		if b == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}

	if pipelineKeyLine < 1 || pipelineKeyLine > len(lineStarts) {
		return 0, 0, fmt.Errorf("pipeline key line %d out of range", pipelineKeyLine)
	}
	start = lineStarts[pipelineKeyLine-1]

	if nextKeyLine == 0 {
		end = len(innerYAML)
	} else {
		if nextKeyLine < 1 || nextKeyLine > len(lineStarts) {
			return 0, 0, fmt.Errorf("next-key line %d out of range", nextKeyLine)
		}
		end = lineStarts[nextKeyLine-1]
	}
	return start, end, nil
}
