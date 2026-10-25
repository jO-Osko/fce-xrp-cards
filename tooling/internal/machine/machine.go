// Package machine talks to a TEE machine's tee-proxy over its external API:
// the signed TEE info a machine registers with, action results and their
// signatures, availability proofs, and direct actions.
package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/machinemanager"
	"github.com/flare-foundation/go-flare-common/pkg/signing"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs/fdc2"
	"github.com/flare-foundation/tee-node/pkg/fdc"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// maxBody bounds what fce reads from a proxy; tee-proxy refuses results over 25 MiB.
const maxBody = 32 << 20

// Proxy is a machine's tee-proxy, at the base URL of its external API.
type Proxy struct {
	URL    string
	Client *http.Client
}

// New is the proxy at url, with a client that gives up on a call after timeout.
func New(url string, timeout time.Duration) Proxy {
	return Proxy{URL: strings.TrimRight(url, "/"), Client: &http.Client{Timeout: timeout}}
}

// ErrNotFound means the proxy has no such result, or none yet.
var ErrNotFound = errors.New("not found")

func (p Proxy) do(ctx context.Context, method, path string, header http.Header, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, p.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	maps.Copy(req.Header, header)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // a failed close changes nothing read
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("reading %s %s: %w", method, path, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, firstLine(b))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decoding %s %s: %w", method, path, err)
	}
	return nil
}

// firstLine is the start of an error body, short enough for one line.
func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// Info is the machine's latest TEE info, signed by the TEE and by the proxy.
func (p Proxy) Info(ctx context.Context) (*types.SignedTeeInfoResponse, error) {
	var info types.SignedTeeInfoResponse
	if err := p.do(ctx, http.MethodGet, "/info", nil, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Result is the action's result under tag; ErrNotFound until there is one.
func (p Proxy) Result(ctx context.Context, id common.Hash, tag types.SubmissionTag) (*types.ActionResponse, error) {
	var r types.ActionResponse
	if err := p.do(ctx, http.MethodGet, "/action/result/"+id.Hex()+"?submissionTag="+string(tag), nil, nil, &r); err != nil {
		return nil, err
	}
	if r.Result.ID != id || r.Result.SubmissionTag != tag {
		return nil, fmt.Errorf("asked for %s under %s, got %s under %s", id, tag, r.Result.ID, r.Result.SubmissionTag)
	}
	return &r, nil
}

// Direct queues a direct action, authorized by apiKey, and returns its id.
func (p Proxy) Direct(ctx context.Context, apiKey string, opType, opCommand common.Hash, message []byte) (common.Hash, error) {
	body, err := json.Marshal(types.DirectInstruction{OPType: opType, OPCommand: opCommand, Message: message})
	if err != nil {
		return common.Hash{}, err
	}
	var a types.Action
	if err := p.do(ctx, http.MethodPost, "/direct", http.Header{"X-Api-Key": {apiKey}}, body, &a); err != nil {
		return common.Hash{}, err
	}
	return a.Data.ID, nil
}

// TeeID is the TEE's id: the address of its public key.
func TeeID(k types.PublicKey) (common.Address, error) {
	pub, err := crypto.UnmarshalPubkey(append([]byte{4}, append(k.X[:], k.Y[:]...)...))
	if err != nil {
		return common.Address{}, fmt.Errorf("the TEE's public key: %w", err)
	}
	return crypto.PubkeyToAddress(*pub), nil
}

// signer recovers who signed TextHash(Payload{prefix, chainID, dataHash}), as
// tee-node and tee-proxy sign.
func signer(prefix signing.Prefix, chainID uint64, dataHash common.Hash, sig []byte) (common.Address, error) {
	h, err := signing.NewPayload(prefix, chainID, dataHash).Hash()
	if err != nil {
		return common.Address{}, err
	}
	pub, err := crypto.SigToPub(accounts.TextHash(h[:]), sig)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

// ProxyID is the proxy's id: the signer of the TEE info's proxy signature.
func ProxyID(info *types.SignedTeeInfoResponse) (common.Address, error) {
	h, err := info.TeeInfo.Hash()
	if err != nil {
		return common.Address{}, err
	}
	a, err := signer(signing.ProxyTeeInfo, info.TeeInfo.ChainID, common.BytesToHash(h), info.ProxySignature)
	if err != nil {
		return common.Address{}, fmt.Errorf("the TEE info's proxy signature: %w", err)
	}
	return a, nil
}

// Registration is what MachineManager.register takes from the machine's TEE info.
func Registration(info *types.SignedTeeInfoResponse) (machinemanager.IMachineManagerTeeMachineData, machinemanager.Signature, error) {
	m := info.MachineData
	data := machinemanager.IMachineManagerTeeMachineData{
		ExtensionId:    m.ExtensionID.Big(),
		InitialOwner:   m.InitialOwner,
		CodeHash:       m.CodeHash,
		Platform:       m.Platform,
		PublicKey:      machinemanager.PublicKey{X: m.PublicKey.X, Y: m.PublicKey.Y},
		GovernanceHash: m.GovernanceHash,
	}
	s := info.DataSignature
	if len(s) != 65 || s[64] > 1 {
		return data, machinemanager.Signature{}, fmt.Errorf("the machine data's signature is not 65 bytes ending in 0 or 1: %s", s)
	}
	// tee-node signs R ‖ S ‖ V with V 0 or 1; the contract takes V 27 or 28
	return data, machinemanager.Signature{V: s[64] + 27, R: [32]byte(s[:32]), S: [32]byte(s[32:64])}, nil
}

// Verify checks that the TEE tee signed the result, and the proxy proxy served it.
func Verify(r *types.ActionResponse, chainID uint64, tee, proxy common.Address) error {
	h := common.BytesToHash(r.Result.Hash())
	if a, err := signer(signing.TEEActionResult, chainID, h, r.Signature); err != nil || a != tee {
		return fmt.Errorf("the result is not signed by TEE %s (recovered %s, %v)", tee, a, err)
	}
	if a, err := signer(signing.ProxyActionResult, chainID, h, r.ProxySignature); err != nil || a != proxy {
		return fmt.Errorf("the result is not signed by proxy %s (recovered %s, %v)", proxy, a, err)
	}
	return nil
}

// Proof is the availability proof MachineManager.toProduction takes, from an
// attester's F_FDC2 PROVE result.
func Proof(r *types.ActionResponse) (machinemanager.ITeeAvailabilityCheckProof, error) {
	var proof machinemanager.ITeeAvailabilityCheckProof
	var pr fdc.ProveResponse
	if err := json.Unmarshal(r.Result.Data, &pr); err != nil {
		return proof, fmt.Errorf("decoding the attester's result: %w", err)
	}
	header, err := fdc.DecodeResponse(pr.ResponseHeader)
	if err != nil {
		return proof, fmt.Errorf("decoding the response header: %w", err)
	}
	args := fdc2.AttestationTypeArguments[fdc2.AvailabilityCheck]
	var req fdc2.ITeeAvailabilityCheckRequestBody
	if err := structs.DecodeTo(args.Request, pr.RequestBody, &req); err != nil {
		return proof, fmt.Errorf("decoding the request body: %w", err)
	}
	var resp fdc2.ITeeAvailabilityCheckResponseBody
	if err := structs.DecodeTo(args.Response, pr.ResponseBody, &resp); err != nil {
		return proof, fmt.Errorf("decoding the response body: %w", err)
	}
	proof = machinemanager.ITeeAvailabilityCheckProof{
		Signatures:  machinemanager.IFdc2VerificationFdc2Signatures{SigningPolicySignatures: pr.DataProviderSignatures},
		Header:      machinemanager.IFdc2HubFdc2ResponseHeader(header),
		RequestBody: machinemanager.ITeeAvailabilityCheckRequestBody(req),
		ResponseBody: machinemanager.ITeeAvailabilityCheckResponseBody{
			Status:                 resp.Status,
			TeeTimestamp:           resp.TeeTimestamp,
			CodeHash:               resp.CodeHash,
			Platform:               resp.Platform,
			InitialSigningPolicyId: resp.InitialSigningPolicyId,
			LastSigningPolicyId:    resp.LastSigningPolicyId,
			State:                  machinemanager.ITeeAvailabilityCheckTeeState(resp.State),
		},
	}
	return proof, nil
}
