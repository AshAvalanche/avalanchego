// SPDX-License-Identifier: MIT

pragma solidity = 0.8.28;

interface IWarpMessenger {
    function sendWarpMessage(bytes calldata payload) external returns (bytes32 messageID);

    function getVerifiedWarpMessage(uint32 index)
        external
        view
        returns (WarpMessage memory message, bool valid);
}

struct WarpMessage {
    bytes32 sourceChainID;
    address originSenderAddress;
    bytes payload;
}

/// @notice Moves the caller's own AVAX to the P-Chain, owned by the caller's
/// warp owner.
///
/// A contract has no key, so it cannot sign an atomic export - and an atomic
/// transaction lives outside any call frame anyway, so there is nowhere for a
/// contract to emit one from. This precompile runs inside an EVM transaction,
/// where msg.sender cannot be forged, so the output's owner is forced to the
/// caller.
interface INativeExport {
    function exportAVAX() external payable;
}

/// @notice A contract that owns AVAX on the P-Chain and stakes it, with no
/// private key anywhere.
///
/// The contract's own address is the warp owner. Funding it needs nothing from
/// it - anyone may name it as the recipient of an atomic export - but spending
/// needs its assent, which it gives by committing to the exact bytes of a
/// P-Chain transaction.
contract WarpOwner {
    IWarpMessenger constant WARP = IWarpMessenger(0x0200000000000000000000000000000000000005);
    INativeExport constant EXPORT = INativeExport(0x0200000000000000000000000000000000000006);

    /// @dev The P-Chain payload type ids this contract speaks.
    uint32 constant TX_AUTHORIZATION = 4;
    uint32 constant TX_EXECUTED = 5;

    address public immutable owner;

    /// @notice authorized[authHash] is the expiry an authorization was issued
    /// with, or zero. It is what lets executed() tie an attestation back to
    /// something this contract actually asked for.
    mapping(bytes32 => uint64) public authorized;

    /// @notice executed[authHash] is the txID the P-Chain accepted for that
    /// authorization, or zero.
    ///
    /// The txID is information the contract could not have had: it commits to
    /// the *unsigned* bytes, while the txID hashes the signed ones, credential
    /// included. Two submissions of one authorization therefore produce two
    /// txIDs, and only the P-Chain can say which was accepted.
    mapping(bytes32 => bytes32) public executed;

    event Authorized(bytes32 indexed authHash, uint64 expiry);
    event Executed(bytes32 indexed authHash, bytes32 txID);

    error NotOwner();
    error NoVerifiedMessage();
    error NotFromPChain();
    error WrongPayloadType();
    error UnknownAuthorization();
    error AlreadyExecuted();

    constructor(address owner_) {
        owner = owner_;
    }

    /// @notice The credit of an import arrives without any code running - no
    /// receive(), no fallback, like a selfdestruct. This exists only so the
    /// contract can be funded in EVM terms; the P-Chain side needs nothing.
    receive() external payable {}

    /// @notice Sends [amount] of the contract's own AVAX to the P-Chain.
    ///
    /// This is the leg an EOA could not perform on the contract's behalf. The
    /// funding path needs nothing from the contract - anyone may name it as an
    /// atomic export's recipient - but moving its *own* balance is its own
    /// decision, so this is owner-only.
    ///
    /// The precompile takes no argument: it debits msg.value and records the
    /// deposit. The P-Chain import that follows is canonical, so anyone
    /// rebuilds it from what they read in shared memory.
    function exportToPChain(uint256 amount) external {
        require(msg.sender == owner, "not the owner");
        EXPORT.exportAVAX{value: amount}();
    }

    /// @notice Authorize a P-Chain transaction by committing to its exact
    /// unsigned bytes.
    ///
    /// The payload carries the transaction itself rather than a description of
    /// it: an intent would leave whoever assembles the transaction free to
    /// choose where the change goes. It carries the preimage rather than a
    /// digest so the submitter can rebuild the transaction from the log alone,
    /// with no out-of-band channel.
    ///
    /// The precompile forces sourceAddress to the caller - this contract - so
    /// the emitted pair is exactly the warp owner the P-Chain UTXOs name.
    function authorize(uint64 expiry, bytes calldata txBytes) external returns (bytes32 authHash) {
        if (msg.sender != owner) {
            revert NotOwner();
        }

        authHash = sha256(txBytes);
        authorized[authHash] = expiry;

        // codecID ‖ typeID ‖ expiry ‖ len(txBytes) ‖ txBytes, big-endian, which
        // is what message.TxAuthorization serializes to.
        WARP.sendWarpMessage(
            abi.encodePacked(
                uint16(0),
                TX_AUTHORIZATION,
                expiry,
                uint32(txBytes.length),
                txBytes
            )
        );

        emit Authorized(authHash, expiry);
    }

    /// @notice Record the txID the P-Chain accepted, from a TxExecuted
    /// attestation the deliverer supplies.
    ///
    /// Callable by anyone, and it has to be: the predicate holding the verified
    /// message belongs to the *transaction*, not to the call, so delivery is
    /// necessarily a top-level EVM transaction built by whoever relays it.
    function recordExecution(uint32 index) external {
        (WarpMessage memory message, bool valid) = WARP.getVerifiedWarpMessage(index);

        // An absent or invalid predicate does not revert - it returns valid =
        // false. A contract that skips this check accepts an unverified
        // message.
        if (!valid) {
            revert NoVerifiedMessage();
        }

        // Both of these are meaningful zeroes: the P-Chain's ID is the zero ID,
        // and an attestation's AddressedCall carries no source address. The
        // usual defensive reflex - reject zero - rejects every attestation.
        if (message.sourceChainID != bytes32(0) || message.originSenderAddress != address(0)) {
            revert NotFromPChain();
        }

        bytes memory payload = message.payload;
        if (payload.length != 70 || _uint32At(payload, 2) != TX_EXECUTED) {
            revert WrongPayloadType();
        }

        // codecID(2) ‖ typeID(4) ‖ txID(32) ‖ authHash(32)
        bytes32 txID = _bytes32At(payload, 6);
        bytes32 authHash = _bytes32At(payload, 38);

        if (authorized[authHash] == 0) {
            revert UnknownAuthorization();
        }
        // An attestation does not expire - an accepted fact stays true - so it
        // is up to the contract to consume it once. Omitting this is how a
        // replayed reward gets credited twice.
        if (executed[authHash] != bytes32(0)) {
            revert AlreadyExecuted();
        }

        executed[authHash] = txID;
        emit Executed(authHash, txID);
    }

    function _uint32At(bytes memory b, uint256 offset) private pure returns (uint32 v) {
        for (uint256 i = 0; i < 4; i++) {
            v = (v << 8) | uint8(b[offset + i]);
        }
    }

    function _bytes32At(bytes memory b, uint256 offset) private pure returns (bytes32 v) {
        assembly {
            v := mload(add(add(b, 32), offset))
        }
    }
}
