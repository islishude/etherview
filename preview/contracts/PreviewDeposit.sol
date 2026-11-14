// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

/// @notice Disposable Preview fixture, NOT a staking deposit contract.
/// Emits EIP-6110-shaped events without verifying BLS signatures or deposit roots.
contract PreviewDeposit {
    event DepositEvent(bytes pubkey, bytes withdrawal_credentials, bytes amount, bytes signature, bytes index);
    uint64 public depositCount;

    function littleEndian(uint64 value) private pure returns (bytes memory out) {
        out = new bytes(8);
        for (uint256 i; i < 8; ++i) out[i] = bytes1(uint8(value >> (8 * i)));
    }

    function deposit(bytes calldata pubkey, bytes calldata credentials, bytes calldata signature) external payable {
        require(pubkey.length == 48 && credentials.length == 32 && signature.length == 96, "Invalid lengths");
        require(msg.value >= 1 ether && msg.value % 1 gwei == 0 && msg.value / 1 gwei <= type(uint64).max, "Invalid amount");
        emit DepositEvent(pubkey, credentials, littleEndian(uint64(msg.value / 1 gwei)), signature, littleEndian(depositCount++));
    }
}
