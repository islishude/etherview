// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

contract PreviewMetadataNFT {
    address private immutable tokenOwner;

    event Transfer(address indexed from, address indexed to, uint256 indexed tokenId);

    constructor() {
        tokenOwner = msg.sender;
        emit Transfer(address(0), msg.sender, 1);
    }

    function supportsInterface(bytes4 interfaceId) external pure returns (bool) {
        return interfaceId == 0x01ffc9a7 || interfaceId == 0x80ac58cd;
    }

    function ownerOf(uint256 tokenId) external view returns (address) {
        require(tokenId == 1, "missing token");
        return tokenOwner;
    }

    function tokenURI(uint256 tokenId) external pure returns (string memory) {
        require(tokenId == 1, "missing token");
        return "ipfs://bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym/metadata.json";
    }
}
