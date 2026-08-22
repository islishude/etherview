// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {CWIA} from "solady/src/utils/legacy/CWIA.sol";

contract MyAccount is CWIA {
    error Unauthorized();

    uint256 public stored;

    function owner() public pure returns (address) {
        return _getArgAddress(0);
    }

    function number() public pure returns (uint256) {
        return _getArgUint256(20);
    }

    function getInfo() external pure returns (address owner_, uint256 number_) {
        owner_ = _getArgAddress(0);
        number_ = _getArgUint256(20);
    }

    function data() public pure returns (bytes memory) {
        uint256 length = _getArgUint16(52);
        return _getArgBytes(54, length);
    }

    function setStored(uint256 next) external {
        if (msg.sender != _getArgAddress(0)) revert Unauthorized();
        stored = next;
    }
}
