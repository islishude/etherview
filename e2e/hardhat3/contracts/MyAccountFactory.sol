// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {LibCWIA} from "solady/src/utils/legacy/LibCWIA.sol";
import {MyAccount} from "./MyAccount.sol";

contract MyAccountFactory {
    error DataTooLarge();

    address public immutable implementation;
    address public account;

    event AccountCreated(
        address indexed account,
        address indexed owner,
        uint256 number
    );

    constructor() {
        implementation = address(new MyAccount());
    }

    function create(
        address owner,
        uint256 number,
        bytes calldata data
    ) external returns (address instance) {
        if (data.length > type(uint16).max) revert DataTooLarge();
        bytes memory immutableArgs = abi.encodePacked(
            owner,
            number,
            uint16(data.length),
            data
        );
        instance = LibCWIA.clone(implementation, immutableArgs);
        account = instance;
        emit AccountCreated(instance, owner, number);
    }
}
