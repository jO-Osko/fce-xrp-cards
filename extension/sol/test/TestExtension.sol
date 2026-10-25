// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { IInstructions } from "flare-periphery/src/coston2/IInstructions.sol";
import { ExtensionBase } from "../src/ExtensionBase.sol";

/// @notice An extension contract for ExtensionBase's tests: a function per way to send, or to misuse
/// _send, and one checking a TEE's signature.
contract TestExtension is ExtensionBase {
    bytes32 public constant OP_TYPE = "TEST";
    bytes32 public constant OP_COMMAND = "ONE";

    constructor(address _manager) ExtensionBase(_manager) { }

    function sendOne(bytes calldata _message) public payable chargesFees returns (bytes32) {
        return _send(OP_TYPE, OP_COMMAND, _message);
    }

    function sendTo(address[] calldata _teeIds, bytes calldata _message)
        external
        payable
        chargesFees
        returns (bytes32)
    {
        return _send(
            _teeIds,
            IInstructions.TeeInstructionParams({
                opType: OP_TYPE,
                opCommand: OP_COMMAND,
                message: _message,
                cosigners: new address[](0),
                cosignersThreshold: 0,
                claimBackAddress: msg.sender
            })
        );
    }

    function sendToWithDefaults(address[] calldata _teeIds, bytes calldata _message)
        external
        payable
        chargesFees
        returns (bytes32)
    {
        return _send(_teeIds, OP_TYPE, OP_COMMAND, _message);
    }

    function teeSigner(bytes32 _digest, bytes calldata _signature) external view returns (address) {
        return _teeSigner(_digest, _signature);
    }

    function sendTwo() external payable chargesFees {
        _send(OP_TYPE, "FIRST", "a");
        _send(OP_TYPE, "SECOND", "b");
    }

    function sendUncharged() external payable {
        _send(OP_TYPE, OP_COMMAND, "a");
    }

    function sendNested(bytes calldata _message) external payable chargesFees {
        sendOne(_message);
    }
}

/// @notice A caller, or a recipient, that refuses plain payments.
contract NoReceive {
    function send(TestExtension _ext, uint256 _value) external {
        _ext.sendOne{ value: _value }("x");
    }
}
