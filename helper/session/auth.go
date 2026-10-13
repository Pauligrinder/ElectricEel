package main

import "electric-eel-session/internal/phoneprotocol"

// Compatibility names for the legacy dispatcher; wire rules live in one package.
const (
	authLevelNone   = phoneprotocol.AuthLevelNone
	authLevelUnlock = phoneprotocol.AuthLevelUnlock
	authLevelDrive  = phoneprotocol.AuthLevelDrive
)

type authenticationRequest = phoneprotocol.AuthenticationRequest

var (
	authLevelName                 = phoneprotocol.AuthLevelName
	parseAuthenticationRequest    = phoneprotocol.ParseAuthenticationRequest
	parseAuthRequestFromFromVCSEC = phoneprotocol.ParseAuthRequestFromFromVCSEC
	parseAuthRequestMessage       = phoneprotocol.ParseAuthRequestMessage
	encodeAuthenticationResponse  = phoneprotocol.EncodeAuthenticationResponse
	sendAuthenticationResponse    = phoneprotocol.SendAuthenticationResponse
	sessionDroppedError           = phoneprotocol.SessionDroppedError
)
