// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import { MockFlareTeeManager } from "fcc-extension-scaffold/test/MockFlareTeeManager.sol";
import { IFlareContractRegistry } from "flare-periphery/src/coston2/IFlareContractRegistry.sol";
import { IMachineManager } from "flare-periphery/src/coston2/IMachineManager.sol";
import { IWalletKeyManager } from "flare-periphery/src/coston2/IWalletKeyManager.sol";
import { IWalletManager } from "flare-periphery/src/coston2/IWalletManager.sol";
import { Test } from "forge-std/src/Test.sol";
import { Deploy } from "../script/Deploy.s.sol";
import { PostRegister } from "../script/PostRegister.s.sol";
import { InstructionSender } from "../src/InstructionSender.sol";

contract InstructionSenderTest is Test {
    // the fake sign port's wallet, which the fixtures sign with
    bytes32 internal constant WALLET = keccak256("fakenode wallet");
    bytes32 internal constant PROJECT = keccak256("project");
    // other keys a wallet can have
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 internal constant KEY_TYPE_XRP = bytes32("XRP");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 internal constant SIGNING_ALGO_XRP = bytes32("sha512half-secp256k1-ecdsa");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 internal constant SIGNING_ALGO_VRF = bytes32("keccak256-secp256k1-vrf");

    MockFlareTeeManager internal manager;
    InstructionSender internal sender;
    uint256 internal fee;
    address internal tee;
    address internal otherTee = address(0x7EF);
    address internal projectOwner = makeAddr("projectOwner");
    address internal signer = makeAddr("signer");
    address internal stranger = makeAddr("stranger");

    function setUp() public {
        manager = new MockFlareTeeManager();
        sender = new InstructionSender(address(manager));
        manager.setInstructionsSender(address(sender));
        sender.setExtensionId(manager.EXTENSION_ID());
        fee = manager.FEE_PER_TEE();
        tee = manager.TEE();
        manager.setTee(otherTee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.PRODUCTION);

        manager.setProject(
            PROJECT, projectOwner, manager.EXTENSION_ID(), sender.KEY_TYPE_EVM(), sender.SIGNING_ALGO_ECDSA()
        );
        setWallet(IWalletManager.WalletStatus.PRODUCTION, new address[](0), 0);
        manager.setWalletKey(WALLET, 0, one(tee));
        vm.prank(projectOwner);
        sender.setSigner(WALLET, signer);
        vm.deal(signer, 1 ether);
        vm.deal(stranger, 1 ether);
    }

    function setWallet(IWalletManager.WalletStatus _status, address[] memory _cosigners, uint64 _threshold) internal {
        manager.setWallet(WALLET, PROJECT, _status, _cosigners, _threshold, 1);
    }

    function transfer(uint256 _chainId) internal pure returns (InstructionSender.Transaction memory) {
        return InstructionSender.Transaction({
            chainId: _chainId,
            to: address(0xB0B),
            value: 1 ether,
            data: "",
            gas: 21_000,
            maxFeePerGas: 2 gwei,
            maxPriorityFeePerGas: 1 gwei
        });
    }

    // the Go side's call(): a contract call on Coston2
    function call() internal pure returns (InstructionSender.Transaction memory) {
        return InstructionSender.Transaction({
            chainId: 114,
            to: address(0xC0DE0),
            value: 0,
            data: hex"a9059cbb000000000000000000000000000000000000000000000000000000000000b0b00000000000000000000000000000000000000000000000000000000000000064",
            gas: 100_000,
            maxFeePerGas: 50 gwei,
            maxPriorityFeePerGas: 0
        });
    }

    function sign(InstructionSender.Transaction memory _transaction) internal returns (bytes32 _id, uint64 _nonce) {
        vm.prank(signer);
        return sender.signTransaction{ value: fee }(WALLET, _transaction);
    }

    function sentNonce() internal view returns (uint64 _nonce) {
        (,, _nonce,) = abi.decode(manager.sentMessage(), (bytes32, uint64, uint64, InstructionSender.Transaction));
    }

    // The contract builds exactly the op pair and payload the Go side handles: the conformance
    // fixtures', from the same fields as the Go side, each the first transaction on its chain.
    function test_MatchesTheFixtures() public {
        string[2] memory names = ["../conformance/1-sign-transfer.json", "../conformance/2-sign-call.json"];
        InstructionSender.Transaction[2] memory transactions = [transfer(31337), call()];
        for (uint256 i; i < names.length; ++i) {
            string memory fixture = vm.readFile(names[i]);
            sign(transactions[i]);
            (bytes32 opType, bytes32 opCommand,,,) = manager.sent();
            assertEq(opType, opName(vm.parseJsonString(fixture, ".opType")));
            assertEq(opCommand, opName(vm.parseJsonString(fixture, ".opCommand")));
            assertEq(manager.sentMessage(), vm.parseJsonBytes(fixture, ".message"));
        }
    }

    function test_SignTransactionParams() public {
        address[] memory cosigners = new address[](2);
        cosigners[0] = makeAddr("cosigner0");
        cosigners[1] = makeAddr("cosigner1");
        setWallet(IWalletManager.WalletStatus.PRODUCTION, cosigners, 2);

        // the instruction id is the mock's, unchecked
        vm.expectEmit(false, true, true, true, address(sender));
        emit InstructionSender.TransactionSent(bytes32(0), WALLET, 31337, 0);
        (bytes32 id, uint64 nonce) = sign(transfer(31337));
        assertNotEq(id, bytes32(0));
        assertEq(nonce, 0);

        (bytes32 opType, bytes32 opCommand,, uint64 threshold, address claimBack) = manager.sent();
        assertEq(opType, sender.OP_TYPE_WALLETS());
        assertEq(opCommand, sender.OP_COMMAND_SIGN_TRANSACTION());
        assertEq(manager.sentTeeIds(), one(tee));
        assertEq(manager.sentCosigners(), cosigners);
        assertEq(threshold, 2);
        assertEq(claimBack, signer);
        assertEq(manager.sentMessage(), abi.encode(WALLET, uint64(0), uint64(0), transfer(31337)));
    }

    // One key is one address on every chain, and each chain counts its own nonce.
    function test_NumbersPerWalletAndChain() public {
        (, uint64 nonce) = sign(transfer(31337));
        assertEq(nonce, 0);
        (, nonce) = sign(transfer(31337));
        assertEq(nonce, 1);
        assertEq(sentNonce(), 1);
        (, nonce) = sign(transfer(114));
        assertEq(nonce, 0);
        assertEq(sentNonce(), 0);

        assertEq(sender.nextNonce(WALLET, 31337), 2);
        assertEq(sender.nextNonce(WALLET, 114), 1);
        assertEq(sender.nextNonce(keccak256("another wallet"), 31337), 0);
    }

    function test_ReplaceTransaction() public {
        sign(transfer(31337));
        sign(transfer(31337));

        InstructionSender.Transaction memory bumped = transfer(31337);
        bumped.maxFeePerGas = 4 gwei;
        vm.prank(signer);
        sender.replaceTransaction{ value: fee }(WALLET, 0, bumped);
        assertEq(manager.sentMessage(), abi.encode(WALLET, uint64(0), uint64(0), bumped));
        assertEq(sender.nextNonce(WALLET, 31337), 2, "the counter only moves forward");

        vm.prank(signer);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotNumberedYet.selector, 2));
        sender.replaceTransaction{ value: fee }(WALLET, 2, bumped);
        // numbered on 31337 only
        vm.prank(signer);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotNumberedYet.selector, 0));
        sender.replaceTransaction{ value: fee }(WALLET, 0, transfer(114));

        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.OnlySigner.selector, WALLET));
        sender.replaceTransaction{ value: fee }(WALLET, 0, bumped);
    }

    // A restored key has a holder per TEE; each signs, and each is paid for.
    function test_SendsToEveryHolder() public {
        address[] memory both = new address[](2);
        both[0] = tee;
        both[1] = otherTee;
        manager.setWalletKey(WALLET, 0, both);
        assertEq(sender.quoteSignTransaction(WALLET), 2 * fee);

        vm.prank(signer);
        sender.signTransaction{ value: 2 * fee }(WALLET, transfer(31337));
        assertEq(manager.sentTeeIds(), both);
        assertEq(manager.sentValue(), 2 * fee);

        // a holder out of production gets nothing
        manager.setTee(otherTee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.SUSPENDED);
        assertEq(sender.quoteSignTransaction(WALLET), fee);
        sign(transfer(31337));
        assertEq(manager.sentTeeIds(), one(tee));
    }

    function test_QuoteSignTransaction() public {
        assertEq(sender.quoteSignTransaction(WALLET), fee);
        sender.setProjectFee(5);
        assertEq(sender.quoteSignTransaction(WALLET), fee + 5);
    }

    function test_SetSigner() public {
        vm.expectEmit(address(sender));
        emit InstructionSender.SignerSet(WALLET, stranger);
        vm.prank(projectOwner);
        sender.setSigner(WALLET, stranger);
        assertEq(sender.signers(WALLET), stranger);

        vm.prank(signer);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.OnlySigner.selector, WALLET));
        sender.signTransaction{ value: fee }(WALLET, transfer(31337));

        // the zero address stops it
        vm.prank(projectOwner);
        sender.setSigner(WALLET, address(0));
        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.OnlySigner.selector, WALLET));
        sender.signTransaction{ value: fee }(WALLET, transfer(31337));
    }

    function test_RevertWhen_NotTheProjectOwner() public {
        address extensionOwner = sender.owner();
        address[2] memory notOwners = [signer, extensionOwner];
        for (uint256 i; i < notOwners.length; ++i) {
            vm.prank(notOwners[i]);
            vm.expectRevert(abi.encodeWithSelector(InstructionSender.OnlyProjectOwner.selector, WALLET));
            sender.setSigner(WALLET, notOwners[i]);
        }
        // an unknown wallet's project is the zero id, whose owner is the zero address
        vm.prank(projectOwner);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.OnlyProjectOwner.selector, bytes32(0)));
        sender.setSigner(bytes32(0), signer);
    }

    function test_RevertWhen_NotTheSigner() public {
        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.OnlySigner.selector, WALLET));
        sender.signTransaction{ value: fee }(WALLET, transfer(31337));
        assertEq(sender.nextNonce(WALLET, 31337), 0, "a refused call takes no number");
    }

    function test_RevertWhen_NoChainId() public {
        vm.prank(signer);
        vm.expectRevert(InstructionSender.ChainIdRequired.selector);
        sender.signTransaction{ value: fee }(WALLET, transfer(0));
    }

    function test_RevertWhen_AnotherExtensionsWallet() public {
        manager.setProject(
            PROJECT, projectOwner, manager.EXTENSION_ID() + 1, sender.KEY_TYPE_EVM(), sender.SIGNING_ALGO_ECDSA()
        );
        vm.prank(signer);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotThisExtension.selector, WALLET));
        sender.signTransaction{ value: fee }(WALLET, transfer(31337));
    }

    function test_RevertWhen_NotAnEvmSigningKey() public {
        bytes32[2][2] memory kinds = [[KEY_TYPE_XRP, SIGNING_ALGO_XRP], [sender.KEY_TYPE_EVM(), SIGNING_ALGO_VRF]];
        for (uint256 i; i < kinds.length; ++i) {
            manager.setProject(PROJECT, projectOwner, manager.EXTENSION_ID(), kinds[i][0], kinds[i][1]);
            vm.prank(signer);
            vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotAnEvmSigningKey.selector, WALLET));
            sender.signTransaction{ value: fee }(WALLET, transfer(31337));
        }
    }

    function test_RevertWhen_WalletNotInProduction() public {
        IWalletManager.WalletStatus[2] memory statuses =
            [IWalletManager.WalletStatus.INITIALIZED, IWalletManager.WalletStatus.PAUSED];
        for (uint256 i; i < statuses.length; ++i) {
            setWallet(statuses[i], new address[](0), 0);
            vm.prank(signer);
            vm.expectRevert(abi.encodeWithSelector(InstructionSender.WalletNotInProduction.selector, WALLET));
            sender.signTransaction{ value: fee }(WALLET, transfer(31337));
        }
    }

    function test_RevertWhen_NotOneKey() public {
        manager.setWalletKey(WALLET, 1, one(otherTee));
        vm.prank(signer);
        vm.expectRevert(abi.encodeWithSelector(InstructionSender.NotOneKey.selector, WALLET, 2));
        sender.signTransaction{ value: 2 * fee }(WALLET, transfer(31337));
    }

    function test_RevertWhen_NoHolderInProduction() public {
        manager.setTee(tee, manager.EXTENSION_ID(), IMachineManager.TeeStatus.SUSPENDED);
        vm.prank(signer);
        vm.expectRevert(IWalletKeyManager.ThresholdNotMet.selector);
        sender.signTransaction{ value: fee }(WALLET, transfer(31337));
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

    function one(address _teeId) internal pure returns (address[] memory _ids) {
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
