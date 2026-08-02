// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {OwnableUpgradeable} from "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

contract Implementation is Initializable, OwnableUpgradeable, UUPSUpgradeable {
    uint256 public value;

    constructor() {
        _disableInitializers();
    }

    function initialize(address initialOwner, uint256 initialValue) external initializer {
        __Ownable_init(initialOwner);
        value = initialValue;
    }

    function setValue(uint256 next) external {
        value = next;
    }

    function version() external pure virtual returns (uint64) {
        return 1;
    }

    function _authorizeUpgrade(address) internal override onlyOwner {}
}

contract ImplementationV2 is Implementation {
    string public label;

    function reinitializeV2(string calldata nextLabel) external reinitializer(2) {
        label = nextLabel;
    }

    function version() external pure override returns (uint64) {
        return 2;
    }
}

contract BadUUIDImplementation is Initializable {
    uint256 public value;

    function proxiableUUID() external pure returns (bytes32) {
        return bytes32(uint256(1));
    }

    function initialize(uint256 initialValue) external initializer {
        value = initialValue;
    }
}
