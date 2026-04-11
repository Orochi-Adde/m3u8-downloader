package main

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

func PKCS7UnPadding(origData []byte) []byte {
	length := len(origData)
	if length == 0 {
		return origData
	}
	unpadding := int(origData[length-1])
	if length < unpadding {
		return origData
	}
	return origData[:(length - unpadding)]
}

func AesDecrypt(crypted, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	if len(crypted) < blockSize {
		return nil, fmt.Errorf("密文过短")
	}
	if len(crypted)%blockSize != 0 {
		return nil, fmt.Errorf("密文长度不是区块大小的整数倍")
	}

	iv := key[:blockSize]
	blockMode := cipher.NewCBCDecrypter(block, iv)
	origData := make([]byte, len(crypted))
	blockMode.CryptBlocks(origData, crypted)
	origData = PKCS7UnPadding(origData)
	return origData, nil
}
