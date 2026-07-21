//go:build e2e

package gateway_token_auth

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const tokenAuthConfigKey = "WK_GATEWAY_TOKEN_AUTH_ENABLED"

func TestSingleNodeGatewayUsesAuthoritativeUserToken(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster(tokenAuthOptions(1)...)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const uid = "e2e-token-single"
	registerToken(t, ctx, node.APIAddr(), uid, "single-token-v1", frame.APP, frame.DeviceLevelMaster)

	requireConnectAccepted(t, node, uid, "single-token-v1", frame.APP)
	requireConnectRejected(t, node, uid, "single-token-wrong", frame.APP)
	requireConnectRejected(t, node, uid, "single-token-v1", frame.WEB)
	requireConnectRejected(t, node, "e2e-token-missing", "missing-token", frame.APP)

	registerToken(t, ctx, node.APIAddr(), uid, "single-token-v2", frame.APP, frame.DeviceLevelSlave)
	requireConnectRejected(t, node, uid, "single-token-v1", frame.APP)
	requireConnectAccepted(t, node, uid, "single-token-v2", frame.APP)
}

func TestThreeNodeGatewayRoutesTokenAuthorityAcrossNodes(t *testing.T) {
	cluster := suite.New(t).StartThreeNodeCluster(tokenAuthOptions(1, 2, 3)...)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	const uid = "e2e-token-routed"
	registerToken(t, ctx, cluster.MustNode(1).APIAddr(), uid, "routed-token-v1", frame.APP, frame.DeviceLevelMaster)

	requireConnectAccepted(t, cluster.MustNode(2), uid, "routed-token-v1", frame.APP)
	requireConnectRejected(t, cluster.MustNode(3), uid, "routed-token-wrong", frame.APP)
	requireConnectRejected(t, cluster.MustNode(2), uid, "routed-token-v1", frame.PC)

	registerToken(t, ctx, cluster.MustNode(3).APIAddr(), uid, "routed-token-v2", frame.APP, frame.DeviceLevelSlave)
	requireConnectRejected(t, cluster.MustNode(1), uid, "routed-token-v1", frame.APP)
	requireConnectAccepted(t, cluster.MustNode(2), uid, "routed-token-v2", frame.APP)
}

func tokenAuthOptions(nodeIDs ...uint64) []suite.Option {
	options := make([]suite.Option, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		options = append(options, suite.WithNodeConfigOverrides(nodeID, map[string]string{
			tokenAuthConfigKey: "true",
		}))
	}
	return options
}

func registerToken(
	t *testing.T,
	ctx context.Context,
	apiAddr, uid, token string,
	deviceFlag frame.DeviceFlag,
	deviceLevel frame.DeviceLevel,
) {
	t.Helper()
	_, err := suite.PostJSON(ctx, "http://"+apiAddr+"/user/token", map[string]any{
		"uid":          uid,
		"token":        token,
		"device_flag":  uint8(deviceFlag),
		"device_level": uint8(deviceLevel),
	}, nil)
	require.NoError(t, err)
}

func requireConnectAccepted(
	t *testing.T,
	node *suite.StartedNode,
	uid, token string,
	deviceFlag frame.DeviceFlag,
) {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	require.NoError(t, client.ConnectWithToken(
		node.GatewayAddr(), uid, uid+"-device", token, deviceFlag,
	), node.DumpDiagnostics())
}

func requireConnectRejected(
	t *testing.T,
	node *suite.StartedNode,
	uid, token string,
	deviceFlag frame.DeviceFlag,
) {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	err = client.ConnectWithToken(node.GatewayAddr(), uid, uid+"-device", token, deviceFlag)
	require.ErrorContains(t, err, frame.ReasonAuthFail.String(), node.DumpDiagnostics())
}
