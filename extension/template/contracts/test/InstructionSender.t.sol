// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { Ownable } from "@openzeppelin-contracts/access/Ownable.sol";
import { MockFlareTeeManager } from "fcc-extension-scaffold/test/MockFlareTeeManager.sol";
import { IFlareContractRegistry } from "flare-periphery/src/coston2/IFlareContractRegistry.sol";
import { Test } from "forge-std/src/Test.sol";
import { Deploy } from "../script/Deploy.s.sol";
import { PostRegister } from "../script/PostRegister.s.sol";
import { InstructionSender } from "../src/InstructionSender.sol";

contract InstructionSenderTest is Test {
    MockFlareTeeManager internal manager;
    InstructionSender internal sender;
    uint256 internal fee;

    function setUp() public {
        manager = new MockFlareTeeManager();
        sender = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(sender));
        sender.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        vm.deal(address(this), 1 ether);
    }

    function test_Constructor() public view {
        assertEq(sender.flareTeeManager(), address(manager));
        assertEq(sender.extensionId(), manager.EXTENSION_ID());
    }

    function test_SettleDailyParams() public {
        bytes32 id = sender.settleDaily{ value: fee }();
        assertTrue(id != bytes32(0));

        (bytes32 opType, bytes32 opCommand,, uint64 threshold, address claimBack) = manager.sent();
        assertEq(opType, sender.OP_TYPE_CARD());
        assertEq(opCommand, sender.OP_COMMAND_SETTLE_DAILY());
        assertEq(manager.sentTeeIds()[0], manager.TEE());
        assertEq(manager.sentCosigners().length, 0);
        assertEq(threshold, 0);
        assertEq(claimBack, address(this));
        assertEq(manager.sentMessage(), abi.encode(sender.SETTLE_DAILY_VERSION()));
    }

    function test_SettleConfirmParams() public {
        bytes32 id = sender.settleConfirm{ value: fee }("0000000000000001", "SETTLETX1");
        assertTrue(id != bytes32(0));

        (bytes32 opType, bytes32 opCommand,,,) = manager.sent();
        assertEq(opType, sender.OP_TYPE_CARD());
        assertEq(opCommand, sender.OP_COMMAND_SETTLE_CONFIRM());
        assertEq(manager.sentMessage(), abi.encode("0000000000000001", "SETTLETX1"));
    }

    function test_SettleDailyOnlyOwner() public {
        address stranger = makeAddr("stranger");
        vm.deal(stranger, 1 ether);
        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, stranger));
        sender.settleDaily{ value: fee }();
    }

    function test_SettleConfirmOnlyOwner() public {
        address stranger = makeAddr("stranger");
        vm.deal(stranger, 1 ether);
        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, stranger));
        sender.settleConfirm{ value: fee }("1", "tx");
    }

    function test_SettleConfirmRejectsEmptyFields() public {
        vm.expectRevert(InstructionSender.EmptySettleNonce.selector);
        sender.settleConfirm{ value: fee }("", "tx");

        vm.expectRevert(InstructionSender.EmptyTxHash.selector);
        sender.settleConfirm{ value: fee }("1", "");
    }

    function test_QuoteFee() public view {
        assertEq(sender.quoteFee(sender.OP_TYPE_CARD(), sender.OP_COMMAND_SETTLE_DAILY()), fee);
        assertEq(sender.quoteFee(sender.OP_TYPE_CARD(), sender.OP_COMMAND_SETTLE_CONFIRM()), fee);
    }

    // Both env variables live in this one test: tests run in parallel and share the environment.
    function test_DeployScript() public {
        Deploy d = new Deploy();
        string memory out = "deployments/test-deploy.json";
        vm.setEnv("DEPLOY_OUTPUT", out);

        vm.setEnv("FLARE_TEE_MANAGER", vm.toString(address(0)));
        vm.mockCall(
            address(d.REGISTRY()),
            abi.encodeCall(IFlareContractRegistry.getContractAddressByName, ("FlareTeeManager")),
            abi.encode(address(manager))
        );
        d.run();
        InstructionSender deployed = InstructionSender(vm.parseJsonAddress(vm.readFile(out), ".instructionSender"));
        assertEq(deployed.flareTeeManager(), address(manager));

        MockFlareTeeManager other = new MockFlareTeeManager();
        vm.setEnv("FLARE_TEE_MANAGER", vm.toString(address(other)));
        d.run();
        deployed = InstructionSender(vm.parseJsonAddress(vm.readFile(out), ".instructionSender"));
        assertEq(deployed.flareTeeManager(), address(other));
        vm.removeFile(out);
    }

    function test_PostRegister() public {
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
