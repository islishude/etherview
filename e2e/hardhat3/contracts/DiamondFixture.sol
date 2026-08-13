// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

contract ValueFacet {
    bytes32 private constant VALUE_SLOT = keccak256("etherview.e2e.diamond.value");

    function setValue(uint256 next) external {
        bytes32 slot = VALUE_SLOT;
        assembly ("memory-safe") {
            sstore(slot, next)
        }
    }

    function value() external view returns (uint256 current) {
        bytes32 slot = VALUE_SLOT;
        assembly ("memory-safe") {
            current := sload(slot)
        }
    }
}

contract MathFacet {
    function double(uint256 value_) external pure returns (uint256) {
        return value_ * 2;
    }
}

contract FixtureDiamond {
    struct Facet {
        address facetAddress;
        bytes4[] functionSelectors;
    }

    struct FacetCut {
        address facetAddress;
        uint8 action;
        bytes4[] functionSelectors;
    }

    event DiamondCut(FacetCut[] diamondCut, address init, bytes calldata_);

    mapping(bytes4 selector => address facet) private selectorToFacet;
    mapping(address facet => bytes4[] selectors) private selectorsByFacet;
    address[] private externalFacets;
    bytes4[] private immutableSelectors;

    constructor(address valueFacet, address mathFacet) {
        require(valueFacet.code.length != 0 && mathFacet.code.length != 0, "facet code");

        bytes4[] memory valueSelectors = new bytes4[](2);
        valueSelectors[0] = ValueFacet.setValue.selector;
        valueSelectors[1] = ValueFacet.value.selector;
        _addFacet(valueFacet, valueSelectors);

        bytes4[] memory mathSelectors = new bytes4[](1);
        mathSelectors[0] = MathFacet.double.selector;
        _addFacet(mathFacet, mathSelectors);

        bytes4[] memory directSelectors = new bytes4[](5);
        directSelectors[0] = bytes4(keccak256("facets()"));
        directSelectors[1] = bytes4(keccak256("facetFunctionSelectors(address)"));
        directSelectors[2] = bytes4(keccak256("facetAddresses()"));
        directSelectors[3] = bytes4(keccak256("facetAddress(bytes4)"));
        directSelectors[4] = bytes4(keccak256("supportsInterface(bytes4)"));
        for (uint256 index = 0; index < directSelectors.length; index++) {
            bytes4 selector = directSelectors[index];
            selectorToFacet[selector] = address(this);
            immutableSelectors.push(selector);
        }

        FacetCut[] memory cuts = new FacetCut[](3);
        cuts[0] = FacetCut(valueFacet, 0, valueSelectors);
        cuts[1] = FacetCut(mathFacet, 0, mathSelectors);
        cuts[2] = FacetCut(address(this), 0, directSelectors);
        emit DiamondCut(cuts, address(0), "");
    }

    function facets() external view returns (Facet[] memory rows) {
        rows = new Facet[](externalFacets.length + 1);
        for (uint256 index = 0; index < externalFacets.length; index++) {
            address facet = externalFacets[index];
            rows[index] = Facet(facet, selectorsByFacet[facet]);
        }
        rows[externalFacets.length] = Facet(address(this), immutableSelectors);
    }

    function facetFunctionSelectors(address facet) external view returns (bytes4[] memory) {
        if (facet == address(this)) {
            return immutableSelectors;
        }
        return selectorsByFacet[facet];
    }

    function facetAddresses() external view returns (address[] memory addresses) {
        addresses = new address[](externalFacets.length + 1);
        for (uint256 index = 0; index < externalFacets.length; index++) {
            addresses[index] = externalFacets[index];
        }
        addresses[externalFacets.length] = address(this);
    }

    function facetAddress(bytes4 selector) external view returns (address) {
        return selectorToFacet[selector];
    }

    function supportsInterface(bytes4 interfaceId) external pure returns (bool) {
        return interfaceId == 0x48e2b093;
    }

    function _addFacet(address facet, bytes4[] memory selectors) private {
        externalFacets.push(facet);
        for (uint256 index = 0; index < selectors.length; index++) {
            bytes4 selector = selectors[index];
            require(selectorToFacet[selector] == address(0), "selector exists");
            selectorToFacet[selector] = facet;
            selectorsByFacet[facet].push(selector);
        }
    }

    fallback() external {
        address facet = selectorToFacet[msg.sig];
        require(facet != address(0) && facet != address(this), "selector missing");
        assembly ("memory-safe") {
            calldatacopy(0, 0, calldatasize())
            let success := delegatecall(gas(), facet, 0, calldatasize(), 0, 0)
            returndatacopy(0, 0, returndatasize())
            switch success
            case 0 { revert(0, returndatasize()) }
            default { return(0, returndatasize()) }
        }
    }
}
