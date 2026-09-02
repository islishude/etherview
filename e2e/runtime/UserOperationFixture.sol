// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

// Runtime-only ERC-4337 wire fixture. It intentionally exercises the official
// PackedUserOperation handleOps ABI and EntryPoint events without implementing
// wallet validation or a Bundler mempool.
contract UserOperationFixture {
    struct PackedUserOperation {
        address sender;
        uint256 nonce;
        bytes initCode;
        bytes callData;
        bytes32 accountGasLimits;
        uint256 preVerificationGas;
        bytes32 gasFees;
        bytes paymasterAndData;
        bytes signature;
    }

    event UserOperationEvent(
        bytes32 indexed userOpHash,
        address indexed sender,
        address indexed paymaster,
        uint256 nonce,
        bool success,
        uint256 actualGasCost,
        uint256 actualGasUsed
    );

    event UserOperationRevertReason(
        bytes32 indexed userOpHash,
        address indexed sender,
        uint256 nonce,
        bytes revertReason
    );

    function handleOps(PackedUserOperation[] calldata operations, address payable) external {
        for (uint256 index = 0; index < operations.length; index++) {
            PackedUserOperation calldata operation = operations[index];
            bytes32 userOpHash = keccak256(abi.encode(operation.sender, operation.nonce, operation.callData));
            emit UserOperationRevertReason(
                userOpHash,
                operation.sender,
                operation.nonce,
                abi.encodeWithSignature("Error(string)", "runtime fixture revert")
            );
            emit UserOperationEvent(
                userOpHash,
                operation.sender,
                address(0),
                operation.nonce,
                false,
                1000,
                25000
            );
        }
    }
}
