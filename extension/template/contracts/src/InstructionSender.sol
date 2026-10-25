// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { ExtensionBase } from "fcc-extension-scaffold/src/ExtensionBase.sol";

/// @title InstructionSender
/// @notice Entry contract for the XRPL credit-card extension. REGISTER,
/// CREDIT_DEPOSIT, RESERVE and GET_POSITION are direct actions on tee-proxy.
/// Daily settlement is an on-chain instruction so every settlement has a
/// public Coston2 proof. The contract also registers the extension and owns machines.
/// @dev NOTE: the deployer owns this contract and, through fce deploy, the
/// extension in the registry. Secure that key before production. Settle calls
/// are onlyOwner; the watcher signs with that key and pays the protocol fee.
contract InstructionSender is ExtensionBase {
    /// @notice CARD op type, matching the Go side's OPTypeCard.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_TYPE_CARD = bytes32("CARD");

    /// @notice SETTLE_DAILY, matching the Go side's OPCommandSettleDaily.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_SETTLE_DAILY = bytes32("SETTLE_DAILY");

    /// @notice SETTLE_CONFIRM, matching the Go side's OPCommandSettleConfirm.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_SETTLE_CONFIRM = bytes32("SETTLE_CONFIRM");

    /// @notice Placeholder version in the SETTLE_DAILY message (chain refuses empty messages).
    uint8 public constant SETTLE_DAILY_VERSION = 0;

    event SettleDailySent(bytes32 indexed instructionId);
    event SettleConfirmSent(bytes32 indexed instructionId, string settleNonce, string txHash);

    error EmptySettleNonce();
    error EmptyTxHash();

    /// @param _flareTeeManager The FlareTeeManager diamond's address.
    constructor(address _flareTeeManager) ExtensionBase(_flareTeeManager) { }

    /// @notice Freezes the virtual settlement pile inside the TEE. Pay at least
    /// quoteFee(OP_TYPE_CARD, OP_COMMAND_SETTLE_DAILY); excess is kept.
    function settleDaily() external payable onlyOwner chargesFees returns (bytes32 _instructionId) {
        _instructionId = _send(OP_TYPE_CARD, OP_COMMAND_SETTLE_DAILY, abi.encode(SETTLE_DAILY_VERSION));
        emit SettleDailySent(_instructionId);
    }

    /// @notice Confirms an XRPL RLUSD Payment against a pending settle nonce.
    /// Pay at least quoteFee(OP_TYPE_CARD, OP_COMMAND_SETTLE_CONFIRM); excess is kept.
    function settleConfirm(string calldata _settleNonce, string calldata _txHash)
        external
        payable
        onlyOwner
        chargesFees
        returns (bytes32 _instructionId)
    {
        require(bytes(_settleNonce).length != 0, EmptySettleNonce());
        require(bytes(_txHash).length != 0, EmptyTxHash());
        _instructionId =
            _send(OP_TYPE_CARD, OP_COMMAND_SETTLE_CONFIRM, abi.encode(_settleNonce, _txHash));
        emit SettleConfirmSent(_instructionId, _settleNonce, _txHash);
    }
}
