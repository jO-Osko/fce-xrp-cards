package main

// nodeVars are tee-node's variables the extension also gets.
var nodeVars = []string{"EXTENSION_PORT", "SIGN_PORT", "LOG_LEVEL", "CHAIN_ID", "EXTENSION_ID"}

// extensionEnv is the extension's environment: nodeVars, then names, each only
// when set, so a variable the launcher didn't get never arrives empty.
func extensionEnv(lookup func(string) (string, bool), names []string) []string {
	seen := make(map[string]bool, len(nodeVars)+len(names))
	env := make([]string, 0, len(nodeVars)+len(names))
	for _, list := range [][]string{nodeVars, names} {
		for _, n := range list {
			if seen[n] {
				continue
			}
			seen[n] = true
			if v, ok := lookup(n); ok {
				env = append(env, n+"="+v)
			}
		}
	}
	return env
}
