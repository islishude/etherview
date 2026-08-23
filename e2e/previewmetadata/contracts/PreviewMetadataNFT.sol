// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

contract PreviewMetadataNFT {
    address private immutable tokenOwner;
    bool private metadataUpdated;

    event Transfer(address indexed from, address indexed to, uint256 indexed tokenId);
    event MetadataUpdate(uint256 tokenId);

    constructor() {
        tokenOwner = msg.sender;
        emit Transfer(address(0), msg.sender, 1);
    }

    function supportsInterface(bytes4 interfaceId) external pure returns (bool) {
        return interfaceId == 0x01ffc9a7 || interfaceId == 0x80ac58cd || interfaceId == 0x49064906;
    }

    function ownerOf(uint256 tokenId) external view returns (address) {
        require(tokenId == 1, "missing token");
        return tokenOwner;
    }

    function tokenURI(uint256 tokenId) external view returns (string memory) {
        require(tokenId == 1, "missing token");
        if (metadataUpdated) {
            return "ipfs://bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym/metadata.json?revision=2";
        }
        return "ipfs://bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym/metadata.json";
    }

    function updateMetadataURI() external {
        require(msg.sender == tokenOwner, "not owner");
        metadataUpdated = true;
        emit MetadataUpdate(1);
    }
}
