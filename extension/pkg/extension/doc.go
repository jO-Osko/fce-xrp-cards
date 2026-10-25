// Package extension serves an FCC extension's HTTP API to tee-node.
//
// tee-node calls two endpoints on loopback: POST /action, answered with an
// ActionResult, and GET /state, answered with a TeeState. The extension
// registers a handler per op pair (op type and command), each for exactly one
// channel: instructions or direct actions. A handler answers at once, or hands
// slow work to a Run that posts its results through the node (WithSignPort).
package extension
