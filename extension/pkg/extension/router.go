package extension

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// InstructionHandler handles an instruction whose threshold was reached; the
// payload is i.OriginalMessage. It answers within HandlerTimeout and decides
// per action:
//   - an error: status 0, its text as the public log; wrap secrets with PublicError
//   - bytes and no Run: status 1, the bytes as the result's data
//   - a Run: status 2 with the bytes; the Run continues in the background (WithSignPort)
type InstructionHandler func(ctx context.Context, i *instruction.DataFixed) ([]byte, Run, error)

// DirectHandler handles a direct action; the payload is d.Message.
// It answers as InstructionHandler does.
type DirectHandler func(ctx context.Context, d *types.DirectInstruction) ([]byte, Run, error)

type route struct {
	channel     types.ActionType
	instruction InstructionHandler
	direct      DirectHandler
}

// systemPrefix marks op types reserved for tee-node.
const systemPrefix = "F_"

// RegisterInstructionHandler registers h for instructions with the op pair.
// It panics if the pair is already registered on either channel, if a name is
// empty or longer than 32 bytes, or if the op type starts with F_.
// Register before serving.
func (s *Server) RegisterInstructionHandler(opType op.Type, opCommand op.Command, h InstructionHandler) {
	if h == nil {
		panic("nil instruction handler")
	}
	s.register(opType, opCommand, route{channel: types.Instruction, instruction: h})
}

// RegisterDirectHandler registers h for direct actions with the op pair.
// It panics like RegisterInstructionHandler.
// Register before serving.
func (s *Server) RegisterDirectHandler(opType op.Type, opCommand op.Command, h DirectHandler) {
	if h == nil {
		panic("nil direct handler")
	}
	s.register(opType, opCommand, route{channel: types.Direct, direct: h})
}

// RegisterDefaultInstructionHandler replaces the handler for instructions
// with no registered pair; the built-in one returns ErrNoHandler.
// It panics if h is nil or a default was already registered.
func (s *Server) RegisterDefaultInstructionHandler(h InstructionHandler) {
	if h == nil {
		panic("nil default instruction handler")
	}
	if s.customDefaultInstruction {
		panic("default instruction handler already registered")
	}
	s.defaultInstruction = h
	s.customDefaultInstruction = true
}

// RegisterDefaultDirectHandler replaces the handler for direct actions with
// no registered pair; the built-in one returns ErrNoHandler.
// It panics if h is nil or a default was already registered.
func (s *Server) RegisterDefaultDirectHandler(h DirectHandler) {
	if h == nil {
		panic("nil default direct handler")
	}
	if s.customDefaultDirect {
		panic("default direct handler already registered")
	}
	s.defaultDirect = h
	s.customDefaultDirect = true
}

func (s *Server) register(opType op.Type, opCommand op.Command, r route) {
	checkName("op type", string(opType))
	checkName("op command", string(opCommand))
	if strings.HasPrefix(string(opType), systemPrefix) {
		panic(fmt.Sprintf("op type %q is reserved for the system", opType))
	}

	id := types.OpID{OPType: opType.Hash(), OPCommand: opCommand.Hash()}
	if _, ok := s.routes[id]; ok {
		panic(fmt.Sprintf("op pair %q / %q already registered", opType, opCommand))
	}
	s.routes[id] = r
}

// isSystem mirrors the contracts' check on the first two bytes.
func isSystem(opType common.Hash) bool {
	return opType[0] == systemPrefix[0] && opType[1] == systemPrefix[1]
}

// checkName runs before op's Hash, which panics with a less helpful message.
func checkName(kind, name string) {
	if name == "" {
		panic("empty " + kind)
	}
	if len(name) > 32 {
		panic(fmt.Sprintf("%s %q is longer than 32 bytes", kind, name))
	}
}

func (s *Server) instructionHandler(id types.OpID) InstructionHandler {
	if r, ok := s.routes[id]; ok && r.channel == types.Instruction {
		return r.instruction
	}
	return s.defaultInstruction
}

func (s *Server) directHandler(id types.OpID) DirectHandler {
	if r, ok := s.routes[id]; ok && r.channel == types.Direct {
		return r.direct
	}
	return s.defaultDirect
}
