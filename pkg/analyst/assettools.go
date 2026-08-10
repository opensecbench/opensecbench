package analyst

import (
	"context"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func createAsset(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	pid, err := requireProject(deps, "create_asset")
	if err != nil {
		return "", err
	}
	assetType := stringArg(call, "type")
	location := stringArg(call, "location")
	if assetType == "" || location == "" {
		return "", fmt.Errorf("create_asset requires 'type' and 'location'")
	}

	apps, err := deps.p().ListApplicationsByProject(ctx, pid)
	if err != nil || len(apps) == 0 {
		return "", fmt.Errorf("create_asset: no application found for project")
	}
	appID := apps[0].ID

	asset, _, err := deps.p().UpsertAsset(ctx, store.NewAsset{
		ApplicationID:     appID,
		Type:              assetType,
		Location:          location,
		Status:            model.AssetStatusDiscovered,
		Origin:            model.AssetOriginAgent,
		VerificationState: model.AssetVerificationUnverified,
		Tags:              strSliceArg(call, "tags"),
	})
	if err != nil {
		return "", fmt.Errorf("create_asset: %w", err)
	}
	return jsonify(asset, nil)
}

func updateAssetStatus(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	if _, err := requireProject(deps, "update_asset_status"); err != nil {
		return "", err
	}
	id := stringArg(call, "id")
	status := stringArg(call, "status")
	if id == "" || status == "" {
		return "", fmt.Errorf("update_asset_status requires 'id' and 'status'")
	}
	asset, err := deps.p().UpdateAssetStatus(ctx, id, status)
	if err != nil {
		return "", fmt.Errorf("update_asset_status: %w", err)
	}
	return jsonify(asset, nil)
}

func tagAsset(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	if _, err := requireProject(deps, "tag_asset"); err != nil {
		return "", err
	}
	id := stringArg(call, "id")
	if id == "" {
		return "", fmt.Errorf("tag_asset requires 'id'")
	}
	tags := strSliceArg(call, "tags")
	asset, err := deps.p().SetAssetTags(ctx, id, tags)
	if err != nil {
		return "", fmt.Errorf("tag_asset: %w", err)
	}
	return jsonify(asset, nil)
}

func createLink(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	if _, err := requireProject(deps, "create_link"); err != nil {
		return "", err
	}
	link := model.EntityLink{
		SourceType:   stringArg(call, "source_type"),
		SourceID:     stringArg(call, "source_id"),
		Relationship: stringArg(call, "relationship"),
		TargetType:   stringArg(call, "target_type"),
		TargetID:     stringArg(call, "target_id"),
		Note:         stringArg(call, "note"),
	}
	if link.SourceType == "" || link.SourceID == "" || link.Relationship == "" || link.TargetType == "" || link.TargetID == "" {
		return "", fmt.Errorf("create_link requires source_type, source_id, relationship, target_type, target_id")
	}
	created, err := deps.p().CreateLink(ctx, link)
	if err != nil {
		return "", fmt.Errorf("create_link: %w", err)
	}
	return jsonify(created, nil)
}

func getAssetGraph(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	if _, err := requireProject(deps, "get_asset_graph"); err != nil {
		return "", err
	}
	id := stringArg(call, "id")
	if id == "" {
		return "", fmt.Errorf("get_asset_graph requires 'id'")
	}
	links, err := deps.p().ListLinks(ctx, "asset", id)
	if err != nil {
		return "", fmt.Errorf("get_asset_graph: %w", err)
	}
	return jsonify(links, nil)
}

func strSliceArg(call agent.ToolCall, key string) []string {
	raw, ok := call.Args[key]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		if s, ok := raw.(string); ok {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
