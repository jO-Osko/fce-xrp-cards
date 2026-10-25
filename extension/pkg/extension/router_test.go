package extension

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPanics(t *testing.T) {
	t.Parallel()
	long := op.Type(strings.Repeat("A", 33))
	tests := []struct {
		name     string
		register func(s *Server)
	}{
		{"duplicate instruction pair", func(s *Server) { s.RegisterInstructionHandler(testType, testCommand, echoInstruction) }},
		{"pair taken by the other channel", func(s *Server) { s.RegisterDirectHandler(testType, testCommand, echoDirect) }},
		{"system op type", func(s *Server) { s.RegisterInstructionHandler("F_WALLET", "MINE", echoInstruction) }},
		{"empty op type", func(s *Server) { s.RegisterInstructionHandler("", "MINE", echoInstruction) }},
		{"empty op command", func(s *Server) { s.RegisterDirectHandler("MINE", "", echoDirect) }},
		{"op type over 32 bytes", func(s *Server) { s.RegisterInstructionHandler(long, "MINE", echoInstruction) }},
		{"op command over 32 bytes", func(s *Server) { s.RegisterDirectHandler("MINE", op.Command(long), echoDirect) }},
		{"nil instruction handler", func(s *Server) { s.RegisterInstructionHandler("MINE", "MINE", nil) }},
		{"nil direct handler", func(s *Server) { s.RegisterDirectHandler("MINE", "MINE", nil) }},
		{"nil default instruction handler", func(s *Server) { s.RegisterDefaultInstructionHandler(nil) }},
		{"nil default direct handler", func(s *Server) { s.RegisterDefaultDirectHandler(nil) }},
		{"second default instruction handler", func(s *Server) {
			s.RegisterDefaultInstructionHandler(echoInstruction)
			s.RegisterDefaultInstructionHandler(echoInstruction)
		}},
		{"second default direct handler", func(s *Server) {
			s.RegisterDefaultDirectHandler(echoDirect)
			s.RegisterDefaultDirectHandler(echoDirect)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			require.Panics(t, func() { tt.register(s) })
		})
	}
}

func TestRegisterAcceptsLimits(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name := strings.Repeat("A", 32)
	require.NotPanics(t, func() {
		s.RegisterInstructionHandler(op.Type(name), op.Command(name), echoInstruction)
		s.RegisterDirectHandler(op.Type(name), "OTHER", echoDirect)
	})
}

func TestDefaultHandlers(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	custom := errors.New("custom default")
	s.RegisterDefaultInstructionHandler(func(context.Context, *instruction.DataFixed) ([]byte, Run, error) { return nil, nil, custom })

	res := postAction(t, s, instructionAction(t, "OTHER", "OTHER", []byte("x")))
	assert.Equal(t, uint8(statusFailed), res.Status)
	assert.Equal(t, "error: custom default", res.Log)

	// the direct default stays built in
	res = postAction(t, s, directAction(t, "OTHER", "OTHER", []byte("x")))
	assert.Equal(t, "error: "+ErrNoHandler.Error(), res.Log)
}

func TestRouteLookupChecksChannel(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	id := types.OpID{OPType: testType.Hash(), OPCommand: testDirectCommand.Hash()}

	_, _, err := s.instructionHandler(id)(t.Context(), &instruction.DataFixed{})
	require.ErrorIs(t, err, ErrNoHandler)

	data, _, err := s.directHandler(id)(t.Context(), &types.DirectInstruction{Message: []byte("x")})
	require.NoError(t, err)
	require.Equal(t, []byte("x"), data)

	_, _, err = s.directHandler(types.OpID{OPType: common.Hash{1}})(t.Context(), &types.DirectInstruction{})
	require.ErrorIs(t, err, ErrNoHandler)
}
