// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

contract Implementation {
    uint256 public value;

    function setValue(uint256 next) external {
        value = next;
    }
}
