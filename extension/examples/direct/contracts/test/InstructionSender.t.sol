// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { Ownable } from "@openzeppelin-contracts/access/Ownable.sol";
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
    address internal admin = makeAddr("admin");
    address[] internal tees;

    function setUp() public {
        manager = new MockFlareTeeManager();
        sender = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(sender));
        sender.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        tees.push(manager.TEE());
        tees.push(address(0x7EF));
        // the platform sends only to TEEs in production
        manager.setTee(address(0x7EF), manager.EXTENSION_ID(), IMachineManager.TeeStatus.PRODUCTION);
        vm.deal(address(this), 1 ether);
    }

    // The contract builds exactly the op pair and payload the Go side handles: the conformance fixture's.
    function test_MatchesTheFixture() public {
        string memory fixture = vm.readFile("../conformance/02-set-admin.json");
        bytes memory payload = vm.parseJsonBytes(fixture, ".message");
        (uint64 nonce, address fromFixture) = abi.decode(payload, (uint64, address));
        assertEq(nonce, 1, "the fixture's is the contract's first setAdmin");

        sender.setAdmin{ value: sender.quoteSetAdmin(tees) }(fromFixture, tees);

        (bytes32 opType, bytes32 opCommand,,,) = manager.sent();
        assertEq(opType, opName(vm.parseJsonString(fixture, ".opType")));
        assertEq(opCommand, opName(vm.parseJsonString(fixture, ".opCommand")));
        assertEq(manager.sentMessage(), payload);
    }

    function test_SetAdminParams() public {
        sender.setAdmin{ value: 2 * fee }(admin, tees);

        (bytes32 opType, bytes32 opCommand,, uint64 threshold, address claimBack) = manager.sent();
        assertEq(opType, sender.OP_TYPE_SECRETS());
        assertEq(opCommand, sender.OP_COMMAND_SET_ADMIN());
        assertEq(manager.sentTeeIds(), tees);
        assertEq(manager.sentCosigners().length, 0);
        assertEq(threshold, 0);
        assertEq(claimBack, address(this));
        assertEq(manager.sentMessage(), abi.encode(uint64(1), admin));
    }

    // Each call takes the next number, and records what it set and when.
    function test_SetAdminNumbersCalls() public {
        address[] memory one = new address[](1);
        one[0] = tees[0];
        vm.expectEmit(address(sender));
        emit InstructionSender.AdminSent(1, admin, tees);
        sender.setAdmin{ value: 2 * fee }(admin, tees);
        vm.warp(block.timestamp + 10);
        sender.setAdmin{ value: fee }(address(0), one);

        assertEq(sender.adminNonce(), 2);
        assertEq(manager.sentMessage(), abi.encode(uint64(2), address(0)));
        (address set, uint64 sentAt) = sender.adminSettings(1);
        assertEq(set, admin);
        assertEq(sentAt, block.timestamp - 10);
        (set, sentAt) = sender.adminSettings(2);
        assertEq(set, address(0));
        assertEq(sentAt, block.timestamp);
    }

    function test_QuoteSetAdmin() public {
        assertEq(sender.quoteSetAdmin(tees), 2 * fee);
        sender.setProjectFee(5);
        assertEq(sender.quoteSetAdmin(tees), 2 * fee + 5);
    }

    function test_RevertWhen_NotTheOwner() public {
        address stranger = makeAddr("stranger");
        vm.deal(stranger, 1 ether);
        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, stranger));
        sender.setAdmin{ value: 2 * fee }(stranger, tees);
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
        assertEq(vm.parseJsonAddress(vm.readFile(out), ".stateVerifier"), address(deployed));

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

/// @notice The state verifier: what each TEE reports against what the contract sent it.
contract StateVerifierTest is Test {
    MockFlareTeeManager internal manager;
    InstructionSender internal sender;
    uint256 internal fee;
    address internal tee;
    address internal otherTee = address(0x7EF);
    address internal admin = makeAddr("admin");
    address internal newAdmin = makeAddr("newAdmin");
    bytes32 internal version;

    function setUp() public {
        manager = new MockFlareTeeManager();
        sender = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(sender));
        sender.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        tee = manager.TEE();
        manager.setTee(otherTee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.PRODUCTION);
        version = sender.STATE_VERSION();
        vm.deal(address(this), 1 ether);
    }

    function _setAdminOn(address _a, address _teeId) internal {
        address[] memory ids = new address[](1);
        ids[0] = _teeId;
        sender.setAdmin{ value: fee }(_a, ids);
    }

    function _verify(uint64 _nonce, address _a) internal view returns (bool) {
        return sender.verifyTeeState(tee, version, abi.encode(_nonce, _a));
    }

    // The Go side pins the same bytes: the version's name and one encoded state.
    function test_SharedVector() public view {
        assertEq(version, hex"534543524554535f53544154455f563100000000000000000000000000000000");
        assertEq(
            abi.encode(uint64(7), address(0xa1)),
            hex"000000000000000000000000000000000000000000000000000000000000000700000000000000000000000000000000000000000000000000000000000000a1"
        );
    }

    // A TEE that has applied nothing reports (0, 0): fine until a setAdmin sent to it gets old.
    function test_BootState() public {
        assertTrue(_verify(0, address(0)));
        assertFalse(_verify(0, admin), "no admin before any setAdmin");

        _setAdminOn(admin, tee);
        assertTrue(_verify(0, address(0)), "the setAdmin may still be on its way");
        vm.warp(block.timestamp + sender.MAX_STATE_LAG());
        assertTrue(_verify(0, address(0)));
        vm.warp(block.timestamp + 1);
        assertFalse(_verify(0, address(0)), "withheld for longer than MAX_STATE_LAG");
    }

    function test_Applied() public {
        _setAdminOn(admin, tee);
        assertTrue(_verify(1, admin));
        assertFalse(_verify(1, newAdmin), "another admin than nonce 1 set");
        assertFalse(_verify(2, admin), "a nonce never sent");

        vm.warp(block.timestamp + 365 days);
        assertTrue(_verify(1, admin), "up to date stays fine");
    }

    function test_NotSentToThisTee() public {
        _setAdminOn(admin, otherTee);
        assertFalse(_verify(1, admin));
        assertTrue(sender.verifyTeeState(otherTee, version, abi.encode(uint64(1), admin)));
    }

    // tee-node doesn't order instructions: a TEE that got 2 first refuses 1, and holds 2.
    function test_Reordered() public {
        _setAdminOn(admin, tee);
        _setAdminOn(newAdmin, tee);
        assertTrue(_verify(2, newAdmin));
        assertTrue(_verify(1, admin), "2 may still be on its way");
    }

    // The oldest setAdmin the TEE lacks sets the deadline, not the newest.
    function test_LagCountsFromTheOldestMissing() public {
        uint256 start = block.timestamp;
        _setAdminOn(admin, tee);
        vm.warp(start + 1 hours);
        _setAdminOn(newAdmin, tee);

        vm.warp(start + sender.MAX_STATE_LAG());
        assertTrue(_verify(0, address(0)));
        vm.warp(start + sender.MAX_STATE_LAG() + 1);
        assertFalse(_verify(0, address(0)));
        assertTrue(_verify(1, admin), "behind only on the newer one, sent 1 h ago");
    }

    function test_RefusesOtherEncodings() public {
        _setAdminOn(admin, tee);
        bytes memory good = abi.encode(uint64(1), admin);
        assertFalse(sender.verifyTeeState(tee, bytes32(type(uint256).max), ""), "tee-node's state unavailable");
        assertFalse(sender.verifyTeeState(tee, bytes32(0), ""), "the empty state");
        assertFalse(sender.verifyTeeState(tee, bytes32(0), good), "another version");
        assertFalse(sender.verifyTeeState(tee, version, bytes.concat(good, hex"00")), "trailing bytes");
        assertFalse(sender.verifyTeeState(tee, version, abi.encode(uint256(1) << 64 | 1, admin)), "nonce over 64 bits");
        assertFalse(
            sender.verifyTeeState(tee, version, abi.encode(uint64(1), uint256(1) << 160 | uint160(admin))),
            "dirty address"
        );
    }

    // The registry's checks call it: a revert would block them all.
    function testFuzz_NeverReverts(address _teeId, bytes32 _v, bytes calldata _state) public view {
        sender.verifyTeeState(_teeId, _v, _state);
        sender.verifyTeeState(_teeId, version, _state);
    }

    function testFuzz_NeverRevertsWhenSent(uint64 _nonce, address _a, uint32 _elapsed) public {
        _setAdminOn(admin, tee);
        _setAdminOn(newAdmin, tee);
        vm.warp(block.timestamp + _elapsed);
        _verify(_nonce, _a);
    }
}

/// @notice The auction: what each call sends, to which TEE, and what settles a round.
contract AuctionTest is Test {
    MockFlareTeeManager internal manager;
    InstructionSender internal sender;
    uint256 internal fee;
    address internal tee;
    uint256 internal teeKey;
    address internal otherTee;
    uint256 internal otherKey;
    address internal alice = address(0xa11ce);
    address internal bob = address(0xb0b);
    address internal stranger = makeAddr("stranger");

    uint32 internal constant PERIOD = 1 days;

    function setUp() public {
        manager = new MockFlareTeeManager();
        sender = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(sender));
        sender.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        (tee, teeKey) = makeAddrAndKey("tee");
        (otherTee, otherKey) = makeAddrAndKey("otherTee");
        manager.setTees(_one(tee));
        manager.setTee(otherTee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.PRODUCTION);
        vm.deal(address(this), 1 ether);
        vm.deal(alice, 1 ether);
        vm.deal(bob, 1 ether);
        vm.deal(stranger, 1 ether);
    }

    function _startRound() internal returns (uint64) {
        return sender.startRound{ value: fee }(PERIOD);
    }

    function _bidAs(address _bidder, uint64 _round, bytes memory _ciphertext) internal returns (uint64) {
        vm.prank(_bidder);
        return sender.bid{ value: fee }(_round, _ciphertext);
    }

    function _endAfterDeadline(uint64 _round) internal {
        (, uint64 deadline,,,,,) = sender.rounds(_round);
        vm.warp(deadline + 1);
        sender.endRound{ value: fee }(_round);
    }

    /// @dev The end's result, as the TEE signs it with `_key`.
    function signed(uint256 _key, uint64 _round, uint64 _bids, address _winner, uint256 _amount)
        internal
        view
        returns (bytes memory)
    {
        bytes32 digest = sender.resultDigest(_round, _bids, _winner, _amount);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(_key, MessageHashUtils.toEthSignedMessageHash(digest));
        return abi.encode(_round, _bids, _winner, _amount, abi.encodePacked(r, s, v));
    }

    function _status(uint64 _round) internal view returns (InstructionSender.Status _st) {
        (,,,, _st,,) = sender.rounds(_round);
    }

    // The contract builds exactly the payloads the Go side handles, and settles with the
    // conformance fixture's signed result: the fake sign port's TEE runs the round.
    function test_MatchesTheFixtures() public {
        address fakeTee = 0x2c7536E3605D9C16a7a3D7b1898e529396a65c23;
        manager.setTees(_one(fakeTee));

        string memory fixture = vm.readFile("../conformance/13-start.json");
        assertEq(_startRound(), 1, "the fixture's is the contract's first round");
        _assertSent(fixture);

        string[5] memory bids = ["12-bid-before-start", "14-bid", "15-bid-copied", "16-bid-not-a-bid", "18-bid-raised"];
        for (uint256 i; i < bids.length; ++i) {
            fixture = vm.readFile(string.concat("../conformance/", bids[i], ".json"));
            (uint64 round, uint64 number, address bidder, bytes memory ciphertext) =
                abi.decode(vm.parseJsonBytes(fixture, ".message"), (uint64, uint64, address, bytes));
            assertEq(number, i + 1, "the fixtures number the bids as the contract does");
            _bidAs(bidder, round, ciphertext);
            _assertSent(fixture);
        }

        fixture = vm.readFile("../conformance/19-end.json");
        _endAfterDeadline(1);
        _assertSent(fixture);

        sender.submitResult(1, vm.parseJsonBytes(fixture, ".result.data"));
        (,,,, InstructionSender.Status st, address winner, uint256 amount) = sender.rounds(1);
        assertEq(uint8(st), uint8(InstructionSender.Status.Settled));
        assertEq(winner, alice);
        assertEq(amount, 2000);
    }

    function _assertSent(string memory _fixture) internal view {
        (bytes32 opType, bytes32 opCommand,,,) = manager.sent();
        assertEq(opType, opName(vm.parseJsonString(_fixture, ".opType")));
        assertEq(opCommand, opName(vm.parseJsonString(_fixture, ".opCommand")));
        assertEq(manager.sentMessage(), vm.parseJsonBytes(_fixture, ".message"));
    }

    function test_StartRound() public {
        vm.expectEmit(false, false, false, false, address(sender));
        emit InstructionSender.RoundStarted(1, tee, uint64(block.timestamp) + PERIOD, bytes32(0));
        _startRound();

        (bytes32 opType, bytes32 opCommand,, uint64 threshold, address claimBack) = manager.sent();
        assertEq(opType, sender.OP_TYPE_AUCTION());
        assertEq(opCommand, sender.OP_COMMAND_START());
        assertEq(manager.sentTeeIds(), _one(tee));
        assertEq(threshold, 0);
        assertEq(claimBack, address(this));
        assertEq(manager.sentMessage(), abi.encode(uint64(1)));

        (address teeId, uint64 deadline, uint64 bids, uint64 endedAt, InstructionSender.Status st,,) = sender.rounds(1);
        assertEq(teeId, tee);
        assertEq(deadline, block.timestamp + PERIOD);
        assertEq(bids, 0);
        assertEq(endedAt, 0);
        assertEq(uint8(st), uint8(InstructionSender.Status.Open));
        assertEq(sender.roundCount(), 1);
    }

    // Each round's bids take the next number, and go to the round's TEE, whatever the random pick.
    function test_Bid() public {
        _startRound();
        manager.setTees(_one(otherTee));
        _startRound();

        vm.expectEmit(false, false, false, false, address(sender));
        emit InstructionSender.BidPlaced(1, 1, alice, bytes32(0));
        assertEq(_bidAs(alice, 1, hex"01"), 1);
        assertEq(_bidAs(bob, 1, hex"02"), 2);
        assertEq(_bidAs(alice, 2, hex"03"), 1);
        assertEq(_bidAs(alice, 1, hex"04"), 3, "a bidder may bid again");

        (bytes32 opType, bytes32 opCommand,,, address claimBack) = manager.sent();
        assertEq(opType, sender.OP_TYPE_AUCTION());
        assertEq(opCommand, sender.OP_COMMAND_BID());
        assertEq(manager.sentTeeIds(), _one(tee));
        assertEq(claimBack, alice);
        assertEq(manager.sentMessage(), abi.encode(uint64(1), uint64(3), alice, hex"04"));
        assertEq(sender.bidHashes(1, 2), keccak256(abi.encode(bob, hex"02")));
        (,, uint64 bids,,,,) = sender.rounds(1);
        assertEq(bids, 3);
    }

    function test_RevertWhen_BiddingClosed() public {
        _startRound();
        (, uint64 deadline,,,,,) = sender.rounds(1);
        vm.warp(deadline);
        _bidAs(alice, 1, hex"01");

        vm.warp(deadline + 1);
        vm.prank(alice);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.BiddingClosed.selector, 1, deadline));
        sender.bid{ value: fee }(1, hex"01");
    }

    function test_RevertWhen_BidTooLarge() public {
        _startRound();
        uint256 max = sender.MAX_BID_SIZE();
        _bidAs(alice, 1, new bytes(max));

        vm.prank(alice);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.BidTooLarge.selector, max + 1));
        sender.bid{ value: fee }(1, new bytes(max + 1));
    }

    function test_RevertWhen_NoSuchRound() public {
        vm.prank(alice);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.RoundNotOpen.selector, 1));
        sender.bid{ value: fee }(1, hex"01");
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.RoundNotOpen.selector, 1));
        sender.endRound{ value: fee }(1);
    }

    function test_RevertWhen_ZeroPeriod() public {
        vm.expectRevert(InstructionSender.ZeroPeriod.selector);
        sender.startRound{ value: fee }(0);
    }

    // Only after the deadline, as often as needed, with the count of bids.
    function test_EndRound() public {
        _startRound();
        _bidAs(alice, 1, hex"01");
        _bidAs(bob, 1, hex"02");
        (, uint64 deadline,,,,,) = sender.rounds(1);
        vm.warp(deadline);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.BiddingOpen.selector, 1, deadline));
        sender.endRound{ value: fee }(1);

        vm.warp(deadline + 1);
        sender.endRound{ value: fee }(1);
        (bytes32 opType, bytes32 opCommand,,,) = manager.sent();
        assertEq(opType, sender.OP_TYPE_AUCTION());
        assertEq(opCommand, sender.OP_COMMAND_END());
        assertEq(manager.sentTeeIds(), _one(tee));
        assertEq(manager.sentMessage(), abi.encode(uint64(1), uint64(2)));

        vm.warp(deadline + 100);
        sender.endRound{ value: fee }(1);
        (,,, uint64 endedAt,,,) = sender.rounds(1);
        assertEq(endedAt, deadline + 100);
    }

    // A lost start or bid can be sent again, the bid by anyone, with its bidder and ciphertext.
    function test_Resend() public {
        _startRound();
        _bidAs(alice, 1, hex"01");
        bytes memory bidMessage = manager.sentMessage();

        sender.resendStart{ value: fee }(1);
        (, bytes32 opCommand,,,) = manager.sent();
        assertEq(opCommand, sender.OP_COMMAND_START());
        assertEq(manager.sentMessage(), abi.encode(uint64(1)));

        (, uint64 deadline,,,,,) = sender.rounds(1);
        vm.warp(deadline + 1);
        vm.prank(stranger);
        sender.resendBid{ value: fee }(1, 1, alice, hex"01");
        (, opCommand,,,) = manager.sent();
        assertEq(opCommand, sender.OP_COMMAND_BID());
        assertEq(manager.sentTeeIds(), _one(tee));
        assertEq(manager.sentMessage(), bidMessage, "the same bid, after the deadline");

        vm.startPrank(stranger);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotTheBid.selector, 1, 1));
        sender.resendBid{ value: fee }(1, 1, alice, hex"02");
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotTheBid.selector, 1, 1));
        sender.resendBid{ value: fee }(1, 1, bob, hex"01");
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotTheBid.selector, 1, 2));
        sender.resendBid{ value: fee }(1, 2, alice, hex"01");
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotTheBid.selector, 1, 0));
        sender.resendBid{ value: fee }(1, 0, address(0), "");
        vm.stopPrank();
    }

    function test_SubmitResult() public {
        _startRound();
        _bidAs(alice, 1, hex"01");
        _bidAs(bob, 1, hex"02");
        _endAfterDeadline(1);

        vm.expectEmit(address(sender));
        emit InstructionSender.RoundSettled(1, bob, 1500);
        vm.prank(stranger);
        sender.submitResult(1, signed(teeKey, 1, 2, bob, 1500));

        (,,,, InstructionSender.Status st, address winner, uint256 amount) = sender.rounds(1);
        assertEq(uint8(st), uint8(InstructionSender.Status.Settled));
        assertEq(winner, bob);
        assertEq(amount, 1500);

        bytes memory another = signed(teeKey, 1, 2, alice, 1);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.RoundNotOpen.selector, 1));
        sender.submitResult(1, another);
    }

    function test_SubmitResultWithoutAWinner() public {
        _startRound();
        _endAfterDeadline(1);
        sender.submitResult(1, signed(teeKey, 1, 0, address(0), 0));
        assertEq(uint8(_status(1)), uint8(InstructionSender.Status.Settled));
    }

    function test_RevertWhen_ResultDoesNotMatch() public {
        _startRound();
        _bidAs(alice, 1, hex"01");
        _startRound();
        _endAfterDeadline(1);

        // signed first: expectRevert takes the next call, and signed makes one
        bytes memory otherRound = signed(teeKey, 2, 1, alice, 1);
        bytes memory otherCount = signed(teeKey, 1, 0, address(0), 0);
        bytes memory otherSigner = signed(otherKey, 1, 1, alice, 1);
        bytes memory good = signed(teeKey, 1, 1, alice, 1);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.WrongRound.selector, 2));
        sender.submitResult(1, otherRound);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.WrongBidCount.selector, 0, 1));
        sender.submitResult(1, otherCount);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.WrongTee.selector, otherTee, tee));
        sender.submitResult(1, otherSigner);

        (,,,, bytes memory sig) = abi.decode(good, (uint64, uint64, address, uint256, bytes));
        bytes memory tampered = abi.encode(uint64(1), uint64(1), alice, uint256(2), sig);
        // the digest differs, so the recovered signer is no TEE at all
        vm.expectRevert(IMachineManager.TeeNotFound.selector);
        sender.submitResult(1, tampered);

        manager.setTee(tee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.SUSPENDED);
        vm.expectRevert(abi.encodeWithSelector(ExtensionBase.SignerNotInProduction.selector, tee));
        sender.submitResult(1, good);
    }

    // Before the first end at any time; after it only once RESULT_TIMEOUT passed without a result.
    function test_Cancel() public {
        _startRound();
        vm.expectEmit(address(sender));
        emit InstructionSender.RoundCancelled(1);
        sender.cancelRound(1);
        assertEq(uint8(_status(1)), uint8(InstructionSender.Status.Cancelled));

        vm.prank(alice);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.RoundNotOpen.selector, 1));
        sender.bid{ value: fee }(1, hex"01");

        _startRound();
        _endAfterDeadline(2);
        uint256 allowedAfter = block.timestamp + sender.RESULT_TIMEOUT();
        vm.warp(allowedAfter);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.TooEarlyToCancel.selector, 2, allowedAfter));
        sender.cancelRound(2);
        vm.warp(allowedAfter + 1);
        sender.cancelRound(2);

        bytes memory result = signed(teeKey, 2, 0, address(0), 0);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.RoundNotOpen.selector, 2));
        sender.submitResult(2, result);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.RoundNotOpen.selector, 2));
        sender.cancelRound(2);
    }

    function test_RevertWhen_NotTheOwner() public {
        _startRound();
        bytes memory unauthorized = abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, stranger);
        vm.startPrank(stranger);
        vm.expectRevert(unauthorized);
        sender.startRound{ value: fee }(PERIOD);
        vm.expectRevert(unauthorized);
        sender.resendStart{ value: fee }(1);
        vm.warp(block.timestamp + PERIOD + 1);
        vm.expectRevert(unauthorized);
        sender.endRound{ value: fee }(1);
        vm.expectRevert(unauthorized);
        sender.cancelRound(1);
        vm.stopPrank();
    }

    // The Go side pins the same bytes: the tag, the digest of one message, and the bid size.
    function test_SharedVector() public view {
        assertEq(sender.RESULT_TAG(), hex"41554354494f4e5f524553554c545f5631000000000000000000000000000000");
        assertEq(
            sender.resultDigest(1, 3, address(0xb0b), 1500),
            hex"6af0090a5f67bf593389af127ad0a3ec8bafe2be4890cdfd0230e4e020ff9ac4"
        );
        assertEq(sender.MAX_BID_SIZE(), 369);
    }

    function _one(address _teeId) internal pure returns (address[] memory _ids) {
        _ids = new address[](1);
        _ids[0] = _teeId;
    }
}

/// @dev An op name as Go and Solidity pad it: UTF-8, right-padded to 32 bytes.
function opName(string memory _name) pure returns (bytes32) {
    // casting is safe: conformance refuses fixtures with names over 32 bytes
    // forge-lint: disable-next-line(unsafe-typecast)
    return bytes32(bytes(_name));
}
