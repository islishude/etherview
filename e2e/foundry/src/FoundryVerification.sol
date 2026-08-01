// SPDX-License-Identifier: MIT
pragma solidity =0.8.30;

contract FoundryVerification {
    address public immutable owner;
    uint256 public immutable seed;

    constructor(uint256 seed_) {
        owner = msg.sender;
        seed = seed_;
    }

    function score(uint256 value) external view returns (uint256) {
        return seed + value;
    }
}
