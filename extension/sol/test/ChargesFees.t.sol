// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { Test } from "forge-std/src/Test.sol";
import { ExtensionBase } from "../src/ExtensionBase.sol";
import { MockFlareTeeManager } from "./MockFlareTeeManager.sol";
import { TestExtension } from "./TestExtension.sol";

/// @notice A caller that makes two charged calls in one transaction.
contract SendsTwice {
    TestExtension internal immutable ext;
    uint256 internal immutable fee;

    constructor(TestExtension _ext, uint256 _fee) {
        ext = _ext;
        fee = _fee;
    }

    function send() external {
        ext.sendOne{ value: fee }("first");
        ext.sendOne{ value: fee }("second");
    }
}

contract ChargesFeesTest is Test {
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

    // msg.value pays for every instruction of the call, never the collected fees.
    function test_RevertWhen_PaidForOneOfTwo() public {
        vm.deal(address(ext), 1 ether); // project fees collected earlier
        vm.prank(caller);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.InsufficientFee.selector, fee, 0));
        ext.sendTwo{ value: fee }();
    }

    function test_TwoSendsPaidInFull() public {
        vm.deal(address(ext), 1 ether);
        vm.prank(caller);
        ext.sendTwo{ value: 2 * fee + 7 }();

        assertEq(manager.sentCount(), 2);
        assertEq(address(ext).balance, 1 ether + 7, "the contract keeps the excess");
        assertEq(caller.balance, 1 ether - 2 * fee - 7, "no refund");
    }

    function test_RevertWhen_SendingOutsideChargesFees() public {
        vm.prank(caller);
        vm.expectRevert(ExtensionBase.NotCharging.selector);
        ext.sendUncharged{ value: fee }();
    }

    function test_RevertWhen_Nested() public {
        vm.prank(caller);
        vm.expectRevert(ExtensionBase.NestedCharge.selector);
        ext.sendNested{ value: 2 * fee }("x");
    }

    // the transient flag and budget are reset after each call, not at the end of the transaction
    function test_TwoChargedCallsInOneTransaction() public {
        SendsTwice s = new SendsTwice(ext, fee);
        vm.deal(address(s), 1 ether);

        s.send();
        assertEq(manager.sentCount(), 2);
        assertEq(address(ext).balance, 0, "keeps nothing: no project fee");
        assertEq(address(s).balance, 1 ether - 2 * fee, "each call paid its own instruction");
    }
}
