// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { ECDSA } from "@openzeppelin-contracts/utils/cryptography/ECDSA.sol";
import { MessageHashUtils } from "@openzeppelin-contracts/utils/cryptography/MessageHashUtils.sol";
import { IMachineManager } from "flare-periphery/src/coston2/IMachineManager.sol";
import { Test } from "forge-std/src/Test.sol";
import { ExtensionBase } from "../src/ExtensionBase.sol";
import { MockFlareTeeManager } from "./MockFlareTeeManager.sol";
import { TestExtension } from "./TestExtension.sol";

contract TeeSignerTest is Test {
    MockFlareTeeManager internal manager;
    TestExtension internal ext;
    address internal tee;
    uint256 internal teeKey;

    bytes32 internal constant DIGEST = keccak256("a message the extension signed");

    function setUp() public {
        manager = new MockFlareTeeManager();
        ext = new TestExtension(address(manager));
        manager.setInstructionsSender(address(ext));
        ext.setExtensionId(manager.EXTENSION_ID());
        (tee, teeKey) = makeAddrAndKey("tee");
        manager.setTee(tee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.PRODUCTION);
    }

    /// @dev A signature as the sign port gives it, with V raised to 27 or 28.
    function _sign(uint256 _key, bytes32 _digest) internal pure returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(_key, MessageHashUtils.toEthSignedMessageHash(_digest));
        return abi.encodePacked(r, s, v);
    }

    function test_TeeSigner() public view {
        assertEq(ext.teeSigner(DIGEST, _sign(teeKey, DIGEST)), tee);
    }

    function test_RevertWhen_UnknownSigner() public {
        (, uint256 key) = makeAddrAndKey("stranger");
        bytes memory sig = _sign(key, DIGEST);
        vm.expectRevert(IMachineManager.TeeNotFound.selector);
        ext.teeSigner(DIGEST, sig);
    }

    // Another digest than the one signed recovers some unregistered address.
    function test_RevertWhen_AnotherDigest() public {
        bytes memory sig = _sign(teeKey, keccak256("another message"));
        vm.expectRevert(IMachineManager.TeeNotFound.selector);
        ext.teeSigner(DIGEST, sig);
    }

    // Signed without the EIP-191 prefix, as a wallet key signs, it recovers some other address.
    function test_RevertWhen_NoPrefix() public {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(teeKey, DIGEST);
        bytes memory sig = abi.encodePacked(r, s, v);
        vm.expectRevert(IMachineManager.TeeNotFound.selector);
        ext.teeSigner(DIGEST, sig);
    }

    function test_RevertWhen_AnotherExtensionsTee() public {
        manager.setTee(tee, manager.EXTENSION_ID() + 1, IMachineManager.TeeStatus.PRODUCTION);
        bytes memory sig = _sign(teeKey, DIGEST);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.SignerNotExtensionTee.selector, tee));
        ext.teeSigner(DIGEST, sig);
    }

    // Before setExtensionId, no TEE is this extension's.
    function test_RevertWhen_ExtensionIdNotSet() public {
        TestExtension fresh = new TestExtension(address(manager));
        bytes memory sig = _sign(teeKey, DIGEST);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.SignerNotExtensionTee.selector, tee));
        fresh.teeSigner(DIGEST, sig);
    }

    function test_RevertWhen_NotInProduction() public {
        IMachineManager.TeeStatus[4] memory statuses = [
            IMachineManager.TeeStatus.INITIALIZED,
            IMachineManager.TeeStatus.SUSPENDED,
            IMachineManager.TeeStatus.PAUSED,
            IMachineManager.TeeStatus.BANNED
        ];
        bytes memory sig = _sign(teeKey, DIGEST);
        for (uint256 i; i < statuses.length; ++i) {
            manager.setTee(tee, manager.EXTENSION_ID(), statuses[i]);
            vm.expectRevert(abi.encodeWithSelector(ExtensionBase.SignerNotInProduction.selector, tee));
            ext.teeSigner(DIGEST, sig);
        }
    }

    function test_RevertWhen_EmergencyPaused() public {
        manager.setExtensionEmergencyPaused(manager.EXTENSION_ID(), true);
        bytes memory sig = _sign(teeKey, DIGEST);
        vm.expectRevert(ExtensionBase.ExtensionEmergencyPaused.selector);
        ext.teeSigner(DIGEST, sig);
    }

    // tee-node's sign port gives V 0 or 1: the extension adds 27, or nothing recovers.
    function test_RevertWhen_VNotRaised() public {
        bytes memory sig = _sign(teeKey, DIGEST);
        sig[64] = bytes1(uint8(sig[64]) - 27);
        vm.expectRevert(ECDSA.ECDSAInvalidSignature.selector);
        ext.teeSigner(DIGEST, sig);
    }
}
