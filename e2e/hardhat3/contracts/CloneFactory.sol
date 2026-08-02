// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {Clones} from "@openzeppelin/contracts/proxy/Clones.sol";

contract CloneFactory {
    address public standardClone;
    address public immutableArgsClone;

    function deployStandard(address implementation) external returns (address instance) {
        instance = Clones.clone(implementation);
        standardClone = instance;
    }

    function deployWithImmutableArgs(
        address implementation,
        bytes calldata args
    ) external returns (address instance) {
        instance = Clones.cloneWithImmutableArgs(implementation, args);
        immutableArgsClone = instance;
    }

    function immutableArgs(address instance) external view returns (bytes memory) {
        return Clones.fetchCloneArgs(instance);
    }
}
