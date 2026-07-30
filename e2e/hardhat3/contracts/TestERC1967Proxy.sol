// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

contract TestERC1967Proxy {
    bytes32 private constant IMPLEMENTATION_SLOT =
        bytes32(uint256(keccak256("eip1967.proxy.implementation")) - 1);

    event Upgraded(address indexed implementation);

    constructor(address implementation, bytes memory data) payable {
        _setImplementation(implementation);
        if (data.length != 0) {
            (bool ok, ) = implementation.delegatecall(data);
            require(ok, "initialization failed");
        }
    }

    function upgradeTo(address implementation) external {
        _setImplementation(implementation);
    }

    function _setImplementation(address implementation) private {
        require(implementation.code.length != 0, "implementation has no code");
        bytes32 slot = IMPLEMENTATION_SLOT;
        assembly {
            sstore(slot, implementation)
        }
        emit Upgraded(implementation);
    }

    fallback() external payable {
        bytes32 slot = IMPLEMENTATION_SLOT;
        assembly {
            let implementation := sload(slot)
            calldatacopy(0, 0, calldatasize())
            let result := delegatecall(gas(), implementation, 0, calldatasize(), 0, 0)
            returndatacopy(0, 0, returndatasize())
            switch result
            case 0 { revert(0, returndatasize()) }
            default { return(0, returndatasize()) }
        }
    }

    receive() external payable {}
}
