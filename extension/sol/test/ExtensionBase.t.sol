// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { Ownable } from "@openzeppelin-contracts/access/Ownable.sol";
import { IInstructions } from "flare-periphery/src/coston2/IInstructions.sol";
import { IMachineManager } from "flare-periphery/src/coston2/IMachineManager.sol";
import { ITeeCommonErrors } from "flare-periphery/src/coston2/ITeeCommonErrors.sol";
import { Test } from "forge-std/src/Test.sol";
import { ExtensionBase } from "../src/ExtensionBase.sol";
import { MockFlareTeeManager } from "./MockFlareTeeManager.sol";
import { NoReceive, TestExtension } from "./TestExtension.sol";

contract ExtensionBaseTest is Test {
    MockFlareTeeManager internal manager;
    TestExtension internal ext;
    uint256 internal fee;
    address internal caller = makeAddr("caller");

    function setUp() public {
        manager = new MockFlareTeeManager();
        ext = new TestExtension(address(manager));
        manager.setInstructionsSender(address(ext));
        ext.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        vm.deal(caller, 1 ether);
    }

    function test_DefaultParams() public {
        vm.prank(caller);
        ext.sendOne{ value: fee }("hello");

        (bytes32 opType, bytes32 opCommand,, uint64 threshold, address claimBack) = manager.sent();
        assertEq(opType, ext.OP_TYPE());
        assertEq(opCommand, ext.OP_COMMAND());
        assertEq(manager.sentTeeIds(), _tees(manager.TEE()));
        assertEq(manager.sentCosigners().length, 0);
        assertEq(threshold, 0);
        assertEq(claimBack, caller);
        assertEq(manager.sentMessage(), bytes("hello"));
    }

    function test_ExactProtocolFee() public {
        assertEq(ext.quoteFee(ext.OP_TYPE(), ext.OP_COMMAND()), fee);
        vm.prank(caller);
        ext.sendOne{ value: fee }("x");

        assertEq(manager.sentValue(), fee);
        assertEq(address(ext).balance, 0);
        assertEq(caller.balance, 1 ether - fee);
    }

    function test_ProjectFeeAndExcessKept() public {
        ext.setProjectFee(5 gwei);
        uint256 price = ext.quoteFee(ext.OP_TYPE(), ext.OP_COMMAND());
        assertEq(price, fee + 5 gwei);

        vm.prank(caller);
        ext.sendOne{ value: price + 3 }("x");

        assertEq(manager.sentValue(), fee, "forwards exactly the protocol fee");
        assertEq(address(ext).balance, 5 gwei + 3, "keeps the project fee and the excess");
        assertEq(caller.balance, 1 ether - price - 3, "no refund");
    }

    // The protocol fee is per TEE; the project fee is per instruction.
    function test_SeveralTees() public {
        address[] memory tees = _tees(makeAddr("a"), makeAddr("b"));
        manager.setTees(tees);
        ext.setProjectFee(5 gwei);

        vm.prank(caller);
        ext.sendTo{ value: 2 * fee + 5 gwei }(tees, "x");

        assertEq(manager.sentTeeIds(), tees);
        assertEq(manager.sentValue(), 2 * fee);
        assertEq(address(ext).balance, 5 gwei);
    }

    function test_ChosenTeesWithDefaults() public {
        address[] memory tees = _tees(makeAddr("a"), makeAddr("b"));
        manager.setTees(tees);

        vm.prank(caller);
        ext.sendToWithDefaults{ value: 2 * fee }(tees, "hello");

        (bytes32 opType, bytes32 opCommand,, uint64 threshold, address claimBack) = manager.sent();
        assertEq(opType, ext.OP_TYPE());
        assertEq(opCommand, ext.OP_COMMAND());
        assertEq(manager.sentTeeIds(), tees);
        assertEq(manager.sentCosigners().length, 0);
        assertEq(threshold, 0);
        assertEq(claimBack, caller);
        assertEq(manager.sentMessage(), bytes("hello"));
        assertEq(manager.sentValue(), 2 * fee);
    }

    function test_RevertWhen_TeesRepeat() public {
        address a = makeAddr("a");
        address[] memory tees = _tees(a, makeAddr("b"), a);
        manager.setTees(tees);

        vm.prank(caller);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.DuplicateTee.selector, a));
        ext.sendTo{ value: 1 ether }(tees, "x");
    }

    // Why _send refuses repeats: the platform charges a TEE once and keeps the rest.
    function test_MockChargesARepeatedTeeOnce() public {
        address[] memory tees = _tees(manager.TEE(), manager.TEE());
        IInstructions.TeeInstructionParams memory params;
        (params.opType, params.opCommand, params.message) = ("T", "C", "x");
        vm.deal(address(ext), fee);
        vm.prank(address(ext));
        manager.sendInstructions{ value: fee }(tees, params);
        assertEq(manager.sentTeeIds(), _tees(manager.TEE()));
    }

    // The platform refuses these, so the mock does too: a test sees what the chain would.
    function test_RevertWhen_TheMessageIsEmpty() public {
        vm.prank(caller);
        vm.expectRevert(IInstructions.MessageEmpty.selector);
        ext.sendOne{ value: fee }("");
    }

    function test_MockRefusesAnEmptyOpTypeOrCommand() public {
        address[] memory tees = _tees(manager.TEE());
        IInstructions.TeeInstructionParams memory params;
        (params.opCommand, params.message) = ("C", "x");
        vm.deal(address(ext), fee);
        vm.startPrank(address(ext));
        vm.expectRevert(IInstructions.OperationTypeEmpty.selector);
        manager.sendInstructions{ value: fee }(tees, params);

        (params.opType, params.opCommand) = ("T", bytes32(0));
        vm.expectRevert(IInstructions.OperationCommandEmpty.selector);
        manager.sendInstructions{ value: fee }(tees, params);
        vm.stopPrank();
    }

    function test_RevertWhen_ATeeIsNotInProduction() public {
        address paused = makeAddr("paused");
        address[] memory tees = _tees(makeAddr("a"), paused);
        manager.setTees(tees);
        manager.setTee(paused, manager.EXTENSION_ID(), IMachineManager.TeeStatus.PAUSED);

        vm.prank(caller);
        vm.expectRevert(ITeeCommonErrors.TeeMachineNotAvailable.selector);
        ext.sendTo{ value: 2 * fee }(tees, "x");

        tees[1] = makeAddr("unknown");
        vm.prank(caller);
        vm.expectRevert(IMachineManager.TeeNotFound.selector);
        ext.sendTo{ value: 2 * fee }(tees, "x");
    }

    function test_RevertWhen_NoTeeInProduction() public {
        manager.setTees(new address[](0));
        bytes32 opType = ext.OP_TYPE();
        bytes32 opCommand = ext.OP_COMMAND();

        vm.expectRevert(IMachineManager.TooMany.selector);
        ext.quoteFee(opType, opCommand);

        vm.prank(caller);
        vm.expectRevert(IMachineManager.TooMany.selector);
        ext.sendOne{ value: fee }("x");
    }

    function test_RevertWhen_PaidBelowThePrice() public {
        ext.setProjectFee(5 gwei);
        uint256 price = fee + 5 gwei;
        vm.prank(caller);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.InsufficientFee.selector, price, price - 1));
        ext.sendOne{ value: price - 1 }("x");
    }

    // nothing is paid back, so a caller needn't accept payments
    function test_CallerThatRefusesPayments() public {
        NoReceive c = new NoReceive();
        vm.deal(address(c), 1 ether);
        c.send(ext, fee + 1);
        assertEq(address(ext).balance, 1);
    }

    function test_SetProjectFee() public {
        vm.expectEmit();
        emit ExtensionBase.ProjectFeeSet(7);
        ext.setProjectFee(7);
        assertEq(ext.projectFee(), 7);

        vm.prank(caller);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, caller));
        ext.setProjectFee(0);
    }

    function test_Withdraw() public {
        ext.setProjectFee(5 gwei);
        vm.prank(caller);
        ext.sendOne{ value: fee + 5 gwei + 3 }("x");

        address payable to = payable(makeAddr("to"));
        vm.expectEmit();
        emit ExtensionBase.Withdrawn(to, 5 gwei + 3);
        ext.withdraw(to);
        assertEq(to.balance, 5 gwei + 3, "the project fee and the excess");
        assertEq(address(ext).balance, 0);

        vm.prank(caller);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, caller));
        ext.withdraw(payable(caller));
    }

    function test_RevertWhen_TheWithdrawalFails() public {
        vm.deal(address(ext), 1);
        address noReceive = address(new NoReceive());
        vm.expectRevert(ExtensionBase.WithdrawFailed.selector);
        ext.withdraw(payable(noReceive));
    }

    function test_OwnershipMovesInTwoSteps() public {
        assertEq(ext.owner(), address(this), "the deployer owns it");
        address multisig = makeAddr("multisig");
        ext.transferOwnership(multisig);
        assertEq(ext.owner(), address(this), "until accepted");

        vm.prank(multisig);
        ext.acceptOwnership();
        assertEq(ext.owner(), multisig);
    }

    function test_SetExtensionIdOnce() public {
        TestExtension fresh = new TestExtension(address(manager));
        manager.setInstructionsSender(address(fresh));
        uint256 id = manager.EXTENSION_ID();

        vm.expectEmit();
        emit ExtensionBase.ExtensionIdSet(id);
        fresh.setExtensionId(id);
        assertEq(fresh.extensionId(), id);

        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.ExtensionIdAlreadySet.selector, id));
        fresh.setExtensionId(id);
    }

    // anyone may register an extension naming the contract; only the owner binds an id
    function test_RevertWhen_NotTheOwnerSetsTheExtensionId() public {
        TestExtension fresh = new TestExtension(address(manager));
        manager.setInstructionsSender(address(fresh));
        uint256 id = manager.EXTENSION_ID();
        vm.prank(caller);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, caller));
        fresh.setExtensionId(id);
        assertEq(fresh.extensionId(), 0);
    }

    function test_RevertWhen_NotTheRegisteredSender() public {
        TestExtension fresh = new TestExtension(address(manager));
        uint256 id = manager.EXTENSION_ID();
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.NotInstructionsSender.selector, id));
        fresh.setExtensionId(id);
    }

    function test_RevertWhen_SendingBeforeTheExtensionId() public {
        TestExtension fresh = new TestExtension(address(manager));
        vm.prank(caller);
        vm.expectRevert(ExtensionBase.ExtensionIdNotSet.selector);
        fresh.sendOne{ value: fee }("x");

        bytes32 opType = fresh.OP_TYPE();
        bytes32 opCommand = fresh.OP_COMMAND();
        vm.expectRevert(ExtensionBase.ExtensionIdNotSet.selector);
        fresh.quoteFee(opType, opCommand);
    }

    function test_RevertWhen_TheManagerHasNoCode() public {
        address nothing = makeAddr("nothing");
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.NoCode.selector, nothing));
        new TestExtension(nothing);
    }

    function _tees(address _a) internal pure returns (address[] memory _t) {
        _t = new address[](1);
        _t[0] = _a;
    }

    function _tees(address _a, address _b) internal pure returns (address[] memory _t) {
        _t = new address[](2);
        (_t[0], _t[1]) = (_a, _b);
    }

    function _tees(address _a, address _b, address _c) internal pure returns (address[] memory _t) {
        _t = new address[](3);
        (_t[0], _t[1], _t[2]) = (_a, _b, _c);
    }
}
