package imagecheck

import (
	"fmt"
	"runtime/debug"
)

const (
	teeNodeModule  = "github.com/flare-foundation/tee-node"
	launcherModule = "github.com/flare-foundation/fce-launcher"
)

// launcherResult checks that the ENTRYPOINT runs the scaffold's launcher, whose path a fork keeps.
func launcherResult(bi *debug.BuildInfo) Result {
	r := Result{Name: "binaries: the ENTRYPOINT runs the launcher"}
	if bi.Path != launcherModule {
		r.Advice = fmt.Errorf("it runs %s, not %s", bi.Path, launcherModule)
	}
	return r
}

// goModule is a module as go list -m -json reports it.
type goModule struct {
	Path    string
	Version string
	Sum     string
}

// teeNodeResult checks the image's tee-node against want, go.mod's requirement.
func teeNodeResult(bi *debug.BuildInfo, want *goModule) Result {
	r := Result{Name: "binaries: tee-node at go.mod's version"}
	m := bi.Main
	switch {
	case want == nil:
		r.Advice = fmt.Errorf("go.mod doesn't require %s", teeNodeModule)
	case bi.Path != teeNodeModule+"/cmd/extension":
		r.Advice = fmt.Errorf("--node runs %s, not %s/cmd/extension", bi.Path, teeNodeModule)
	case m.Version != want.Version || m.Sum != want.Sum:
		r.Advice = fmt.Errorf("the image's tee-node is %s %s %s, go.mod requires %s %s",
			m.Path, m.Version, m.Sum, want.Version, want.Sum)
	}
	return r
}
