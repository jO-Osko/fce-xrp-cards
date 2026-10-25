// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { ECDSA } from "@openzeppelin-contracts/utils/cryptography/ECDSA.sol";
import { MessageHashUtils } from "@openzeppelin-contracts/utils/cryptography/MessageHashUtils.sol";
import { ExtensionBase } from "fcc-extension-scaffold/src/ExtensionBase.sol";
import { MockFlareTeeManager } from "fcc-extension-scaffold/test/MockFlareTeeManager.sol";
import { IFlareContractRegistry } from "flare-periphery/src/coston2/IFlareContractRegistry.sol";
import { IMachineManager } from "flare-periphery/src/coston2/IMachineManager.sol";
import { Test } from "forge-std/src/Test.sol";
import { Deploy } from "../script/Deploy.s.sol";
import { PostRegister } from "../script/PostRegister.s.sol";
import { InstructionSender } from "../src/InstructionSender.sol";

contract InstructionSenderTest is Test {
    MockFlareTeeManager internal manager;
    InstructionSender internal sender;
    uint256 internal fee;
    address internal caller = makeAddr("caller");

    function setUp() public {
        manager = new MockFlareTeeManager();
        sender = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(sender));
        sender.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        vm.deal(caller, 1 ether);
    }

    // The contract builds exactly the op pair and payload the Go side handles: the conformance fixture's.
    function test_MatchesTheFixture() public {
        string memory fixture = vm.readFile("../conformance/get-price-unknown-pair.json");
        bytes memory payload = vm.parseJsonBytes(fixture, ".message");
        string memory pair = abi.decode(payload, (string));

        vm.prank(caller);
        sender.getPrice{ value: fee }(pair);

        (bytes32 opType, bytes32 opCommand,,,) = manager.sent();
        assertEq(opType, opName(vm.parseJsonString(fixture, ".opType")));
        assertEq(opCommand, opName(vm.parseJsonString(fixture, ".opCommand")));
        assertEq(manager.sentMessage(), payload);
    }

    function test_GetPriceParams() public {
        vm.prank(caller);
        sender.getPrice{ value: fee }("BTC/USD");

        (bytes32 opType, bytes32 opCommand,, uint64 threshold, address claimBack) = manager.sent();
        assertEq(opType, sender.OP_TYPE_PRICES());
        assertEq(opCommand, sender.OP_COMMAND_GET_PRICE());
        assertEq(manager.sentTeeIds()[0], manager.TEE());
        assertEq(manager.sentCosigners().length, 0);
        assertEq(threshold, 0);
        assertEq(claimBack, caller);
        assertEq(manager.sentMessage(), abi.encode("BTC/USD"));
    }

    // Both env variables live in this one test: tests run in parallel and share the environment.
    function test_DeployScript() public {
        Deploy d = new Deploy();
        string memory out = "deployments/test-deploy.json";
        vm.setEnv("DEPLOY_OUTPUT", out);

        // FlareTeeManager from the registry, whatever the shell exports
        vm.setEnv("FLARE_TEE_MANAGER", vm.toString(address(0)));
        vm.mockCall(
            address(d.REGISTRY()),
            abi.encodeCall(IFlareContractRegistry.getContractAddressByName, ("FlareTeeManager")),
            abi.encode(address(manager))
        );
        d.run();
        InstructionSender deployed = InstructionSender(vm.parseJsonAddress(vm.readFile(out), ".instructionSender"));
        assertEq(deployed.flareTeeManager(), address(manager));

        // flareTeeManager overrides it
        MockFlareTeeManager other = new MockFlareTeeManager();
        vm.setEnv("FLARE_TEE_MANAGER", vm.toString(address(other)));
        d.run();
        deployed = InstructionSender(vm.parseJsonAddress(vm.readFile(out), ".instructionSender"));
        assertEq(deployed.flareTeeManager(), address(other));
        vm.removeFile(out);
    }

    function test_PostRegisterScript() public {
        // the script broadcasts as the deployer, which owns the contract
        vm.prank(DEFAULT_SENDER);
        InstructionSender fresh = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(fresh));
        vm.setEnv("INSTRUCTION_SENDER", vm.toString(address(fresh)));
        vm.setEnv("EXTENSION_ID", vm.toString(manager.EXTENSION_ID()));

        PostRegister p = new PostRegister();
        p.run();
        assertEq(fresh.extensionId(), manager.EXTENSION_ID());
        p.run(); // re-running with the same id changes nothing
    }
}

/// @notice submitPrice: what a signed price must be to be stored.
contract SubmitPriceTest is Test {
    MockFlareTeeManager internal manager;
    InstructionSender internal sender;
    uint256 internal fee;
    address internal tee;
    uint256 internal teeKey;
    address internal otherTee;
    uint256 internal otherKey;

    uint256 internal constant PRICE = 8256370000000;

    function setUp() public {
        manager = new MockFlareTeeManager();
        sender = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(sender));
        sender.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        (tee, teeKey) = makeAddrAndKey("tee");
        (otherTee, otherKey) = makeAddrAndKey("otherTee");
        address[] memory tees = new address[](1);
        tees[0] = tee;
        manager.setTees(tees);
        manager.setTee(otherTee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.PRODUCTION);
        vm.deal(address(this), 1 ether);
    }

    function _request(string memory _pair) internal returns (bytes32) {
        return sender.getPrice{ value: fee }(_pair);
    }

    /// @dev The final result's data, as the TEE signs it with `_key`.
    function signed(uint256 _key, bytes32 _id, string memory _pair, uint256 _price)
        internal
        view
        returns (bytes memory)
    {
        bytes32 digest = sender.priceDigest(_id, _pair, _price, 8, 3);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(_key, MessageHashUtils.toEthSignedMessageHash(digest));
        return abi.encode(_pair, _price, uint8(8), uint8(3), abi.encodePacked(r, s, v));
    }

    // The Go side pins the same bytes: the tag, and the digest of one message.
    function test_SharedVector() public view {
        assertEq(sender.PRICE_TAG(), hex"5052494345535f50524943455f56310000000000000000000000000000000000");
        assertEq(
            sender.priceDigest(bytes32(uint256(0x1d)), "BTC/USD", PRICE, 8, 3),
            hex"f37ebd1bf2584234671d901ee7cc833bc71be0e25274c031697e580d58ba51bb"
        );
    }

    function test_GetPriceRecordsTheRequest() public {
        vm.expectEmit(false, false, false, true, address(sender));
        emit InstructionSender.PriceRequested(bytes32(0), "BTC/USD", tee);
        bytes32 id = _request("BTC/USD");
        (bytes32 pairHash, address teeId, uint64 requestedAt) = sender.requests(id);
        assertEq(pairHash, keccak256("BTC/USD"));
        assertEq(teeId, tee);
        assertEq(requestedAt, block.timestamp);
    }

    function test_SubmitPrice() public {
        bytes32 id = _request("BTC/USD");
        uint64 requestedAt = uint64(block.timestamp);
        vm.warp(block.timestamp + 90);

        vm.expectEmit(address(sender));
        emit InstructionSender.PriceSubmitted(id, "BTC/USD", PRICE, requestedAt);
        sender.submitPrice(id, signed(teeKey, id, "BTC/USD", PRICE));

        InstructionSender.Price memory got = sender.latestPrice("BTC/USD");
        assertEq(got.price, PRICE);
        assertEq(got.decimals, 8);
        assertEq(got.sources, 3);
        assertEq(got.requestedAt, requestedAt, "the request's time, not the submission's");
        assertEq(sender.latestPrice("ETH/USD").requestedAt, 0, "none yet");

        bytes memory result = signed(teeKey, id, "BTC/USD", PRICE);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.UnknownRequest.selector, id));
        sender.submitPrice(id, result);
    }

    function test_Deadline() public {
        bytes32 onTime = _request("BTC/USD");
        bytes32 late = _request("ETH/USD");
        uint256 deadline = block.timestamp + sender.MAX_PRICE_DELAY();

        vm.warp(deadline);
        sender.submitPrice(onTime, signed(teeKey, onTime, "BTC/USD", PRICE));
        vm.warp(deadline + 1);
        bytes memory result = signed(teeKey, late, "ETH/USD", PRICE);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.TooLate.selector, late, deadline));
        sender.submitPrice(late, result);
    }

    // Answers may land out of order: the latest request's price stays.
    function test_OlderAnswerNotStored() public {
        bytes32 first = _request("BTC/USD");
        vm.warp(block.timestamp + 60);
        bytes32 second = _request("BTC/USD");

        sender.submitPrice(second, signed(teeKey, second, "BTC/USD", PRICE));
        sender.submitPrice(first, signed(teeKey, first, "BTC/USD", PRICE - 1));
        assertEq(sender.latestPrice("BTC/USD").price, PRICE);
        (, address teeId,) = sender.requests(first);
        assertEq(teeId, address(0), "used up all the same");
    }

    function test_RevertWhen_UnknownRequest() public {
        bytes32 id = keccak256("never requested");
        bytes memory result = signed(teeKey, id, "BTC/USD", PRICE);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.UnknownRequest.selector, id));
        sender.submitPrice(id, result);
    }

    function test_RevertWhen_WrongPair() public {
        bytes32 id = _request("BTC/USD");
        bytes memory result = signed(teeKey, id, "ETH/USD", PRICE);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.WrongPair.selector, id));
        sender.submitPrice(id, result);
    }

    // Another price than the one signed recovers some unregistered address.
    function test_RevertWhen_Tampered() public {
        bytes32 id = _request("BTC/USD");
        bytes memory result = signed(teeKey, id, "BTC/USD", PRICE);
        (string memory pair,, uint8 decimals, uint8 sources, bytes memory sig) =
            abi.decode(result, (string, uint256, uint8, uint8, bytes));
        vm.expectRevert(IMachineManager.TeeNotFound.selector);
        sender.submitPrice(id, abi.encode(pair, PRICE + 1, decimals, sources, sig));
    }

    // Signed for another request, the digest differs in the same way.
    function test_RevertWhen_SignedForAnotherRequest() public {
        bytes32 id = _request("BTC/USD");
        bytes32 other = _request("BTC/USD");
        bytes memory result = signed(teeKey, other, "BTC/USD", PRICE);
        vm.expectRevert(IMachineManager.TeeNotFound.selector);
        sender.submitPrice(id, result);
    }

    function test_RevertWhen_AnotherTee() public {
        bytes32 id = _request("BTC/USD");
        bytes memory result = signed(otherKey, id, "BTC/USD", PRICE);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.WrongTee.selector, otherTee, tee));
        sender.submitPrice(id, result);
    }

    function test_RevertWhen_NotInProduction() public {
        bytes32 id = _request("BTC/USD");
        manager.setTee(tee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.SUSPENDED);
        bytes memory result = signed(teeKey, id, "BTC/USD", PRICE);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.SignerNotInProduction.selector, tee));
        sender.submitPrice(id, result);
    }

    function test_RevertWhen_AnotherExtension() public {
        bytes32 id = _request("BTC/USD");
        manager.setTee(tee, 0x10002, IMachineManager.TeeStatus.PRODUCTION);
        bytes memory result = signed(teeKey, id, "BTC/USD", PRICE);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.SignerNotExtensionTee.selector, tee));
        sender.submitPrice(id, result);
    }

    function test_RevertWhen_EmergencyPaused() public {
        bytes32 id = _request("BTC/USD");
        manager.setExtensionEmergencyPaused(manager.EXTENSION_ID(), true);
        bytes memory result = signed(teeKey, id, "BTC/USD", PRICE);
        vm.expectRevert(ExtensionBase.ExtensionEmergencyPaused.selector);
        sender.submitPrice(id, result);
    }

    // tee-node's sign port gives V 0 or 1: the Go side adds 27, or nothing recovers.
    function test_RevertWhen_VNotAdjusted() public {
        bytes32 id = _request("BTC/USD");
        bytes memory result = signed(teeKey, id, "BTC/USD", PRICE);
        (string memory pair, uint256 price, uint8 decimals, uint8 sources, bytes memory sig) =
            abi.decode(result, (string, uint256, uint8, uint8, bytes));
        sig[64] = bytes1(uint8(sig[64]) - 27);
        vm.expectRevert(ECDSA.ECDSAInvalidSignature.selector);
        sender.submitPrice(id, abi.encode(pair, price, decimals, sources, sig));
    }
}

/// @dev An op name as Go and Solidity pad it: UTF-8, right-padded to 32 bytes.
function opName(string memory _name) pure returns (bytes32) {
    // casting is safe: conformance refuses fixtures with names over 32 bytes
    // forge-lint: disable-next-line(unsafe-typecast)
    return bytes32(bytes(_name));
}
