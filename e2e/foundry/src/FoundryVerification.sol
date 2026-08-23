// SPDX-License-Identifier: MIT
pragma solidity =0.8.30;

contract FoundryDerivedChild {
    address public immutable factory;
    uint256 public immutable seed;

    constructor(uint256 seed_) {
        factory = msg.sender;
        seed = seed_;
    }
}

contract FoundryVerification {
    address public immutable owner;
    uint256 public immutable seed;
    address public immutable child;

    constructor(uint256 seed_) {
        owner = msg.sender;
        seed = seed_;
        child = address(new FoundryDerivedChild(seed_ + 1));
    }

    function score(uint256 value) external view returns (uint256) {
        return seed + value;
    }
}
