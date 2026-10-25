// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { IInstructions } from "flare-periphery/src/coston2/IInstructions.sol";
import { IMachineManager } from "flare-periphery/src/coston2/IMachineManager.sol";
import { ITeeCommonErrors } from "flare-periphery/src/coston2/ITeeCommonErrors.sol";
import { TeeIdKeyIdPair } from "flare-periphery/src/coston2/ITeeIdKeyIdPair.sol";
import { IWalletKeyManager } from "flare-periphery/src/coston2/IWalletKeyManager.sol";
import { IWalletManager } from "flare-periphery/src/coston2/IWalletManager.sol";

/// @title MockFlareTeeManager
/// @notice The FlareTeeManager facets an entrypoint contract calls, for forge tests. As the real
/// ones: a fee per TEE, FeeTooLow below it, duplicate TEEs removed before charging, all of
/// msg.value kept, only the extension's registered instructions sender may send, no empty op
/// type, op command or message, every TEE sent to in production, TooMany when asked for more TEEs
/// than are in production, each TEE's extension and status, TeeNotFound for an unknown one, and
/// the extension's emergency pause flag. Wallets: projects, wallets and their keys' holders, read
/// as the real getters read them, zeros for an unknown id.
/// Not modelled: system senders and op types, the emergency pause and TEEs of another extension
/// on a send, the grace window after an emergency pause, wallet setup.
contract MockFlareTeeManager {
    uint256 public constant EXTENSION_ID = 0x10001;
    uint256 public constant FEE_PER_TEE = 1 gwei;
    address public constant TEE = address(0x7EE);

    address public instructionsSender;

    // the extension's TEEs in production
    address[] internal tees;

    // every TEE the mock knows: its extension and status; NONE is unknown
    mapping(address teeId => uint256) internal extensionOf;
    mapping(address teeId => IMachineManager.TeeStatus) internal statusOf;
    mapping(uint256 extensionId => bool) public isExtensionEmergencyPaused;

    struct Project {
        address owner;
        uint256 extensionId;
        bytes32 keyType;
        bytes32 signingAlgo;
    }

    struct Wallet {
        bytes32 projectId;
        IWalletManager.WalletStatus status;
        address[] cosigners;
        uint64 cosignersThreshold;
        uint64 multisigThreshold;
        uint64[] keyIds;
    }

    mapping(bytes32 projectId => Project) internal projects;
    mapping(bytes32 walletId => Wallet) internal wallets;
    mapping(bytes32 walletId => mapping(uint64 keyId => address[])) internal keyTees;

    // the last instruction sent
    address[] internal lastTeeIds;
    IInstructions.TeeInstructionParams public sent;
    uint256 public sentValue;
    uint256 public sentCount;

    constructor() {
        tees.push(TEE);
        setTee(TEE, EXTENSION_ID, IMachineManager.TeeStatus.PRODUCTION);
    }

    function setInstructionsSender(address _sender) external {
        instructionsSender = _sender;
    }

    /// @notice Replaces the TEEs in production, TEE alone at first; none makes getRandomTeeIds revert.
    /// Each becomes a PRODUCTION TEE of EXTENSION_ID; the TEEs it replaces keep their status.
    function setTees(address[] calldata _teeIds) external {
        tees = _teeIds;
        for (uint256 i; i < _teeIds.length; ++i) {
            setTee(_teeIds[i], EXTENSION_ID, IMachineManager.TeeStatus.PRODUCTION);
        }
    }

    /// @notice Registers a TEE, or changes its extension or status; NONE forgets it.
    function setTee(address _teeId, uint256 _extensionId, IMachineManager.TeeStatus _status) public {
        extensionOf[_teeId] = _extensionId;
        statusOf[_teeId] = _status;
    }

    function setExtensionEmergencyPaused(uint256 _extensionId, bool _paused) external {
        isExtensionEmergencyPaused[_extensionId] = _paused;
    }

    function setProject(
        bytes32 _projectId,
        address _owner,
        uint256 _extensionId,
        bytes32 _keyType,
        bytes32 _signingAlgo
    ) external {
        projects[_projectId] = Project(_owner, _extensionId, _keyType, _signingAlgo);
    }

    function setWallet(
        bytes32 _walletId,
        bytes32 _projectId,
        IWalletManager.WalletStatus _status,
        address[] calldata _cosigners,
        uint64 _cosignersThreshold,
        uint64 _multisigThreshold
    ) external {
        Wallet storage w = wallets[_walletId];
        w.projectId = _projectId;
        w.status = _status;
        w.cosigners = _cosigners;
        w.cosignersThreshold = _cosignersThreshold;
        w.multisigThreshold = _multisigThreshold;
    }

    /// @notice Adds a key to a wallet, or replaces its holders.
    function setWalletKey(bytes32 _walletId, uint64 _keyId, address[] calldata _teeIds) external {
        uint64[] storage keyIds = wallets[_walletId].keyIds;
        bool known;
        for (uint256 i; i < keyIds.length && !known; ++i) {
            known = keyIds[i] == _keyId;
        }
        if (!known) keyIds.push(_keyId);
        keyTees[_walletId][_keyId] = _teeIds;
    }

    function getWalletProjectId(bytes32 _walletId) external view returns (bytes32) {
        return wallets[_walletId].projectId;
    }

    function getWalletStatus(bytes32 _walletId) external view returns (IWalletManager.WalletStatus) {
        return wallets[_walletId].status;
    }

    function getWalletCosignersAndThreshold(bytes32 _walletId) external view returns (address[] memory, uint64) {
        return (wallets[_walletId].cosigners, wallets[_walletId].cosignersThreshold);
    }

    /// @notice The counter is the number of keys: keys are never deleted here.
    function getWalletKeysInfo(bytes32 _walletId)
        external
        view
        returns (uint64 _multisigThreshold, uint64[] memory _keyIds, uint64 _counter)
    {
        Wallet storage w = wallets[_walletId];
        return (w.multisigThreshold, w.keyIds, uint64(w.keyIds.length));
    }

    function getOwner(bytes32 _projectId) external view returns (address) {
        return projects[_projectId].owner;
    }

    function getExtensionId(bytes32 _projectId) external view returns (uint256) {
        return projects[_projectId].extensionId;
    }

    function getKeyType(bytes32 _projectId) external view returns (bytes32) {
        return projects[_projectId].keyType;
    }

    function getSigningAlgo(bytes32 _projectId) external view returns (bytes32) {
        return projects[_projectId].signingAlgo;
    }

    /// @notice As the real one: a pair per key and holder in production, key by key; ThresholdNotMet
    /// when fewer keys than the multisig threshold have one. A view here; not on the real diamond,
    /// which emits WalletKeysNotAvailable for the keys without one.
    function receivingTeesAndKeys(bytes32 _walletId) public view returns (TeeIdKeyIdPair[] memory _pairs) {
        Wallet storage w = wallets[_walletId];
        uint256 count;
        uint256 available;
        for (uint256 i; i < w.keyIds.length; ++i) {
            uint256 before = count;
            address[] storage holders = keyTees[_walletId][w.keyIds[i]];
            for (uint256 j; j < holders.length; ++j) {
                if (statusOf[holders[j]] == IMachineManager.TeeStatus.PRODUCTION) ++count;
            }
            if (count > before) ++available;
        }
        require(available >= w.multisigThreshold, IWalletKeyManager.ThresholdNotMet());

        _pairs = new TeeIdKeyIdPair[](count);
        uint256 n;
        for (uint256 i; i < w.keyIds.length; ++i) {
            address[] storage holders = keyTees[_walletId][w.keyIds[i]];
            for (uint256 j; j < holders.length; ++j) {
                if (statusOf[holders[j]] == IMachineManager.TeeStatus.PRODUCTION) {
                    _pairs[n++] = TeeIdKeyIdPair(holders[j], w.keyIds[i]);
                }
            }
        }
    }

    /// @notice receivingTeesAndKeys' TEEs, each once.
    function getReceivingTeeIds(bytes32 _walletId) external view returns (address[] memory _teeIds) {
        TeeIdKeyIdPair[] memory pairs = receivingTeesAndKeys(_walletId);
        _teeIds = new address[](pairs.length);
        for (uint256 i; i < pairs.length; ++i) {
            _teeIds[i] = pairs[i].teeId;
        }
        return _distinct(_teeIds);
    }

    function getExtensionId(address _teeId) external view returns (uint256) {
        require(statusOf[_teeId] != IMachineManager.TeeStatus.NONE, IMachineManager.TeeNotFound());
        return extensionOf[_teeId];
    }

    function getTeeMachineStatus(address _teeId) external view returns (IMachineManager.TeeStatus) {
        require(statusOf[_teeId] != IMachineManager.TeeStatus.NONE, IMachineManager.TeeNotFound());
        return statusOf[_teeId];
    }

    function getTeeExtensionInstructionsSender(uint256 _extensionId) external view returns (address) {
        return _extensionId == EXTENSION_ID ? instructionsSender : address(0);
    }

    /// @notice The first `_count` TEEs in production: distinct, as the real pick, but not random.
    function getRandomTeeIds(uint256 _extensionId, uint256 _count) external view returns (address[] memory _teeIds) {
        require(_extensionId == EXTENSION_ID, "unknown extension");
        require(_count <= tees.length, IMachineManager.TooMany());
        _teeIds = new address[](_count);
        for (uint256 i; i < _count; ++i) {
            _teeIds[i] = tees[i];
        }
    }

    function calculateFeeByTeeIds(bytes32, bytes32, address[] memory _teeIds) public pure returns (uint256) {
        return FEE_PER_TEE * _teeIds.length;
    }

    function sendInstructions(address[] memory _teeIds, IInstructions.TeeInstructionParams calldata _params)
        external
        payable
        returns (bytes32)
    {
        _teeIds = _distinct(_teeIds);
        require(msg.sender == instructionsSender, IInstructions.OnlyInstructionsSender());
        require(_params.opType != bytes32(0), IInstructions.OperationTypeEmpty());
        require(_params.opCommand != bytes32(0), IInstructions.OperationCommandEmpty());
        require(_params.message.length != 0, IInstructions.MessageEmpty());
        for (uint256 i; i < _teeIds.length; ++i) {
            IMachineManager.TeeStatus status = statusOf[_teeIds[i]];
            require(status != IMachineManager.TeeStatus.NONE, IMachineManager.TeeNotFound());
            require(status == IMachineManager.TeeStatus.PRODUCTION, ITeeCommonErrors.TeeMachineNotAvailable());
        }
        require(
            msg.value >= calculateFeeByTeeIds(_params.opType, _params.opCommand, _teeIds), IInstructions.FeeTooLow()
        );
        lastTeeIds = _teeIds;
        sent = _params;
        sentValue = msg.value;
        sentCount++;
        // unique per send, as the real id's counter makes it
        return keccak256(abi.encode(sentCount, _teeIds, _params));
    }

    function sentTeeIds() external view returns (address[] memory) {
        return lastTeeIds;
    }

    function sentMessage() external view returns (bytes memory) {
        return sent.message;
    }

    function sentCosigners() external view returns (address[] memory) {
        return sent.cosigners;
    }

    function _distinct(address[] memory _teeIds) internal pure returns (address[] memory _out) {
        address[] memory kept = new address[](_teeIds.length);
        uint256 n;
        for (uint256 i; i < _teeIds.length; ++i) {
            bool seen;
            for (uint256 j; j < n && !seen; ++j) {
                seen = kept[j] == _teeIds[i];
            }
            if (!seen) kept[n++] = _teeIds[i];
        }
        _out = new address[](n);
        for (uint256 i; i < n; ++i) {
            _out[i] = kept[i];
        }
    }
}
