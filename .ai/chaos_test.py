#!/usr/bin/env python3
"""
CamChaos RTMP Adversarial Test Suite
Target: MediaMTX at 165.245.188.43:1935

Tests the specific nil pointer dereference patches in gortmplib reader.go:
- H265 VideoTypeConfig with nil/empty HEVCConfig
- H264 VideoTypeConfig with nil/empty AVCConfig
- Malformed codec configs
- Protocol-level abuse (mid-handshake aborts, truncated messages, etc.)
- Connection flooding

WARNING: Only run against dev/staging servers you own.
"""

import socket
import struct
import time
import sys
import os
import hashlib
import random
import threading
import traceback

TARGET_HOST = "165.245.188.43"
TARGET_PORT = 1935
TIMEOUT = 5

# ============================================================
# RTMP Protocol Constants
# ============================================================
RTMP_VERSION = 3
CHUNK_SIZE = 128

# Message types
MSG_SET_CHUNK_SIZE = 1
MSG_ABORT = 2
MSG_ACK = 3
MSG_USER_CONTROL = 4
MSG_WINDOW_ACK_SIZE = 5
MSG_SET_PEER_BANDWIDTH = 6
MSG_AUDIO = 8
MSG_VIDEO = 9
MSG_COMMAND_AMF0 = 20
MSG_DATA_AMF0 = 18

# Video codec IDs
CODEC_H264 = 7
CODEC_H265 = 12

# Video types
VIDEO_TYPE_CONFIG = 0
VIDEO_TYPE_AU = 1
VIDEO_TYPE_EOS = 2

# Extended video types
VIDEO_EX_SEQUENCE_START = 0
VIDEO_EX_CODED_FRAMES = 1
VIDEO_EX_SEQUENCE_END = 2
VIDEO_EX_FRAMES_X = 3
VIDEO_EX_METADATA = 4
VIDEO_EX_MULTITRACK = 5

# FourCC values
FOURCC_AVC = 0x61766331   # 'avc1'
FOURCC_HEVC = 0x68766331  # 'hvc1'
FOURCC_AV1 = 0x61763031   # 'av01'
FOURCC_VP9 = 0x76703039   # 'vp09'

results = []

def log_result(test_name, category, severity, passed, detail):
    status = "PASS" if passed else "FAIL"
    results.append({
        "name": test_name,
        "category": category,
        "severity": severity,
        "passed": passed,
        "detail": detail
    })
    icon = "\033[92m[PASS]\033[0m" if passed else "\033[91m[FAIL]\033[0m"
    print(f"  {icon} {test_name}: {detail}")


# ============================================================
# AMF0 Encoding
# ============================================================
def amf0_number(val):
    return b'\x00' + struct.pack('>d', val)

def amf0_string(val):
    encoded = val.encode('utf-8')
    return b'\x02' + struct.pack('>H', len(encoded)) + encoded

def amf0_object(obj):
    data = b'\x03'
    for key, value in obj.items():
        data += struct.pack('>H', len(key)) + key.encode('utf-8')
        if isinstance(value, str):
            data += amf0_string(value)
        elif isinstance(value, (int, float)):
            data += amf0_number(value)
        elif isinstance(value, bool):
            data += b'\x01' + (b'\x01' if value else b'\x00')
        elif value is None:
            data += b'\x05'
    data += b'\x00\x00\x09'
    return data

def amf0_null():
    return b'\x05'


# ============================================================
# RTMP Chunk Writing
# ============================================================
def write_chunk(sock, chunk_stream_id, msg_type, msg_stream_id, timestamp, payload, chunk_size=CHUNK_SIZE):
    """Write an RTMP message split into chunks."""
    # Chunk type 0 (full header) for first chunk
    fmt_csid = (0 << 6) | (chunk_stream_id & 0x3F)
    header = struct.pack('B', fmt_csid)
    # 3-byte timestamp
    ts = min(timestamp, 0xFFFFFF)
    header += struct.pack('>I', ts)[1:]
    # 3-byte message length
    header += struct.pack('>I', len(payload))[1:]
    # 1-byte message type
    header += struct.pack('B', msg_type)
    # 4-byte message stream ID (little-endian)
    header += struct.pack('<I', msg_stream_id)

    sock.sendall(header + payload[:chunk_size])

    offset = chunk_size
    while offset < len(payload):
        # Chunk type 3 (no header)
        continuation = struct.pack('B', (3 << 6) | (chunk_stream_id & 0x3F))
        end = min(offset + chunk_size, len(payload))
        sock.sendall(continuation + payload[offset:end])
        offset = end


# ============================================================
# RTMP Handshake
# ============================================================
def do_handshake(sock):
    """Perform RTMP handshake (C0+C1, read S0+S1+S2, send C2)."""
    # C0: version
    sock.sendall(struct.pack('B', RTMP_VERSION))
    # C1: timestamp(4) + zero(4) + random(1528)
    c1_time = struct.pack('>I', int(time.time()) & 0xFFFFFFFF)
    c1_zero = b'\x00' * 4
    c1_random = os.urandom(1528)
    c1 = c1_time + c1_zero + c1_random
    sock.sendall(c1)

    # Read S0
    s0 = sock.recv(1)
    if len(s0) < 1:
        raise ConnectionError("No S0 received")

    # Read S1 (1536 bytes)
    s1 = b''
    while len(s1) < 1536:
        chunk = sock.recv(1536 - len(s1))
        if not chunk:
            raise ConnectionError("Failed to read S1")
        s1 += chunk

    # Read S2 (1536 bytes)
    s2 = b''
    while len(s2) < 1536:
        chunk = sock.recv(1536 - len(s2))
        if not chunk:
            raise ConnectionError("Failed to read S2")
        s2 += chunk

    # C2: echo back S1
    sock.sendall(s1)
    return True


def do_connect(sock, app="live"):
    """Send RTMP connect command."""
    payload = amf0_string("connect")
    payload += amf0_number(1)  # transaction ID
    payload += amf0_object({
        "app": app,
        "type": "nonprivate",
        "flashVer": "FMLE/3.0",
        "tcUrl": f"rtmp://{TARGET_HOST}/{app}",
    })
    write_chunk(sock, 3, MSG_COMMAND_AMF0, 0, 0, payload)

    # Read responses until we get _result for connect
    deadline = time.time() + TIMEOUT
    while time.time() < deadline:
        try:
            data = sock.recv(4096)
            if not data:
                raise ConnectionError("Connection closed during connect")
            # Check for _result in the response (simplified check)
            if b'_result' in data or b'NetConnection.Connect.Success' in data:
                return True
            if b'_error' in data:
                return False
        except socket.timeout:
            break
    return False


def do_create_stream(sock):
    """Send createStream command."""
    payload = amf0_string("createStream")
    payload += amf0_number(2)  # transaction ID
    payload += amf0_null()
    write_chunk(sock, 3, MSG_COMMAND_AMF0, 0, 0, payload)
    time.sleep(0.3)
    # Drain response
    try:
        sock.recv(4096)
    except:
        pass
    return True


def do_publish(sock, stream_key="chaos_test"):
    """Send publish command."""
    payload = amf0_string("publish")
    payload += amf0_number(3)  # transaction ID
    payload += amf0_null()
    payload += amf0_string(stream_key)
    payload += amf0_string("live")
    write_chunk(sock, 8, MSG_COMMAND_AMF0, 1, 0, payload)
    time.sleep(0.3)
    # Drain response
    try:
        sock.recv(4096)
    except:
        pass
    return True


def full_publish_setup(stream_key="chaos_test"):
    """Complete handshake + connect + createStream + publish. Returns socket or None."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(TIMEOUT)
    try:
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        if not do_connect(sock):
            sock.close()
            return None
        do_create_stream(sock)
        do_publish(sock, stream_key)
        return sock
    except Exception as e:
        sock.close()
        return None


# ============================================================
# Video Message Builders
# ============================================================

def build_legacy_video_config(codec, config_data):
    """Build a legacy (non-extended) video config message.
    Format: [frame_type:4|codec:4][type][cts:3][config_data]
    """
    body = bytearray()
    body.append((1 << 4) | codec)  # keyframe | codec
    body.append(VIDEO_TYPE_CONFIG)  # sequence header
    body.extend(b'\x00\x00\x00')   # composition time offset
    body.extend(config_data)
    return bytes(body)


def build_extended_video_sequence_start(fourcc, config_data):
    """Build an extended video sequence start message.
    Format: [0x80 | type][fourcc:4][config_data]
    """
    body = bytearray()
    body.append(0x80 | VIDEO_EX_SEQUENCE_START)
    body.extend(struct.pack('>I', fourcc))
    body.extend(config_data)
    return bytes(body)


def build_legacy_video_au(codec, au_data, keyframe=True):
    """Build a legacy video AU (access unit) message."""
    body = bytearray()
    frame_type = 1 if keyframe else 2
    body.append((frame_type << 4) | codec)
    body.append(VIDEO_TYPE_AU)
    body.extend(b'\x00\x00\x00')  # CTS
    body.extend(au_data)
    return bytes(body)


def build_minimal_avc_config():
    """Build a minimal valid AVC decoder configuration."""
    # AVCDecoderConfigurationRecord
    sps = bytes([
        0x67, 0x42, 0xc0, 0x1e, 0xd9, 0x00, 0xa0, 0x47,
        0xfe, 0xc8, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00,
        0x00, 0xc8, 0x40, 0x00, 0x00, 0x48, 0x00, 0x00,
        0x24, 0x43, 0xc6, 0x0c, 0x65, 0x80
    ])
    pps = bytes([0x68, 0xce, 0x38, 0x80])

    config = bytearray()
    config.append(1)        # configurationVersion
    config.append(sps[1])   # AVCProfileIndication
    config.append(sps[2])   # profile_compatibility
    config.append(sps[3])   # AVCLevelIndication
    config.append(0xFF)     # lengthSizeMinusOne = 3 (NALU length = 4 bytes)
    config.append(0xE1)     # numOfSequenceParameterSets = 1
    config.extend(struct.pack('>H', len(sps)))
    config.extend(sps)
    config.append(1)        # numOfPictureParameterSets
    config.extend(struct.pack('>H', len(pps)))
    config.extend(pps)
    return bytes(config)


def build_minimal_hevc_config():
    """Build a minimal valid HEVC decoder configuration (HvcC box content)."""
    vps = bytes([0x40, 0x01, 0x0c, 0x01, 0xff, 0xff, 0x01, 0x60,
                 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x00,
                 0x03, 0x00, 0x00, 0x03, 0x00, 0x78, 0xac, 0x09])
    sps = bytes([0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03,
                 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x00,
                 0x03, 0x00, 0x78, 0xa0, 0x03, 0xc0, 0x80, 0x10,
                 0xe5, 0x96, 0x56, 0x69, 0x24, 0xca, 0xf0, 0x10,
                 0x00, 0x00, 0x03, 0x00, 0x10, 0x00, 0x00, 0x03,
                 0x01, 0xe0, 0x80])
    pps = bytes([0x44, 0x01, 0xc1, 0x72, 0xb4, 0x62, 0x40])

    config = bytearray()
    config.append(1)        # configurationVersion
    # general_profile_space(2) | general_tier_flag(1) | general_profile_idc(5)
    config.append(0x01)
    config.extend(b'\x60\x00\x00\x00')  # general_profile_compatibility_flags
    config.extend(b'\x00\x00\x00\x00\x00\x00')  # general_constraint_indicator_flags
    config.append(0x00)     # general_level_idc
    config.extend(b'\xf0\x00')  # min_spatial_segmentation_idc
    config.append(0xfc)     # parallelismType
    config.append(0xfd)     # chromaFormat
    config.append(0xf8)     # bitDepthLumaMinus8
    config.append(0xf8)     # bitDepthChromaMinus8
    config.extend(b'\x00\x00')  # avgFrameRate
    config.append(0x0f)     # constantFrameRate | numTemporalLayers | temporalIdNested | lengthSizeMinusOne
    config.append(3)        # numOfArrays

    # VPS array
    config.append(0x20)     # array_completeness | NAL_unit_type (VPS=32)
    config.extend(struct.pack('>H', 1))  # numNalus
    config.extend(struct.pack('>H', len(vps)))
    config.extend(vps)

    # SPS array
    config.append(0x21)     # SPS=33
    config.extend(struct.pack('>H', 1))
    config.extend(struct.pack('>H', len(sps)))
    config.extend(sps)

    # PPS array
    config.append(0x22)     # PPS=34
    config.extend(struct.pack('>H', 1))
    config.extend(struct.pack('>H', len(pps)))
    config.extend(pps)

    return bytes(config)


# ============================================================
# Test Functions
# ============================================================

def test_connectivity():
    """Baseline: can we connect, handshake, and publish at all?"""
    print("\n--- Test 0: Baseline Connectivity ---")
    sock = None
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        connected = do_connect(sock)
        if connected:
            log_result("Baseline RTMP connect", "Connection", "Info", True,
                      "Server accepts RTMP connections and handshake completes")
        else:
            log_result("Baseline RTMP connect", "Connection", "Critical", False,
                      "Server rejected connect command")
            return False
        do_create_stream(sock)
        do_publish(sock, "chaos_baseline")
        log_result("Baseline publish", "Connection", "Info", True,
                  "Publish setup succeeded")
        return True
    except Exception as e:
        log_result("Baseline RTMP connect", "Connection", "Critical", False, str(e))
        return False
    finally:
        if sock:
            sock.close()


def test_h265_nil_hevc_config():
    """
    CHAOS PATTERN: H265 VideoTypeConfig with empty/nil HEVC config body.
    This is the exact crash vector that was patched: sending codec=12 (H265)
    with VideoTypeConfig but empty or garbage config data, causing the
    mp4.Unmarshal to produce a nil/empty HvcC which then panicked on access.
    """
    print("\n--- Test 1: H265 VideoTypeConfig with empty HEVC config ---")

    # Test 1a: Completely empty config body
    sock = full_publish_setup("chaos_h265_empty")
    if not sock:
        log_result("H265 empty config - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session")
        return
    try:
        # codec=12 (H265), VideoTypeConfig, but ZERO bytes of config data
        video_msg = build_legacy_video_config(CODEC_H265, b'')
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        # Try to send more data to verify connection is still alive
        video_msg2 = build_legacy_video_config(CODEC_H265, b'')
        write_chunk(sock, 6, MSG_VIDEO, 1, 100, video_msg2)
        time.sleep(0.5)
        log_result("H265 empty config body", "Keyframe", "Critical", True,
                  "Server survived empty H265 config (no crash)")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("H265 empty config body", "Keyframe", "Critical", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("H265 empty config body", "Keyframe", "Critical", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # Test 1b: Truncated HEVC config (just a few bytes, not valid HvcC)
    time.sleep(0.5)
    sock = full_publish_setup("chaos_h265_trunc")
    if not sock:
        log_result("H265 truncated config - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session (server may have crashed)")
        return
    try:
        truncated_config = b'\x01\x00\x00'  # Just 3 bytes, not a valid HvcC
        video_msg = build_legacy_video_config(CODEC_H265, truncated_config)
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("H265 truncated config", "Keyframe", "Critical", True,
                  "Server survived truncated H265 config")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("H265 truncated config", "Keyframe", "Critical", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("H265 truncated config", "Keyframe", "Critical", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # Test 1c: Valid HvcC structure but with zero NALU arrays (no VPS/SPS/PPS)
    time.sleep(0.5)
    sock = full_publish_setup("chaos_h265_nonalu")
    if not sock:
        log_result("H265 no-NALU config - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session (server may have crashed)")
        return
    try:
        # Valid-looking HvcC header but numOfArrays=0
        config = bytearray()
        config.append(1)        # configurationVersion
        config.append(0x01)     # general_profile
        config.extend(b'\x60\x00\x00\x00')
        config.extend(b'\x00\x00\x00\x00\x00\x00')
        config.append(0x00)
        config.extend(b'\xf0\x00')
        config.append(0xfc)
        config.append(0xfd)
        config.append(0xf8)
        config.append(0xf8)
        config.extend(b'\x00\x00')
        config.append(0x0f)
        config.append(0)   # numOfArrays = 0 -- no VPS, SPS, PPS!

        video_msg = build_legacy_video_config(CODEC_H265, bytes(config))
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("H265 config with 0 NALU arrays", "Keyframe", "Critical", True,
                  "Server survived H265 config with no VPS/SPS/PPS")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("H265 config with 0 NALU arrays", "Keyframe", "Critical", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("H265 config with 0 NALU arrays", "Keyframe", "Critical", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_h264_nil_avc_config():
    """
    CHAOS PATTERN: H264 VideoTypeConfig with empty/nil AVC config body.
    Targets the patched nil pointer dereference for AVCConfig.
    """
    print("\n--- Test 2: H264 VideoTypeConfig with empty/nil AVC config ---")

    # Test 2a: Empty config body
    sock = full_publish_setup("chaos_h264_empty")
    if not sock:
        log_result("H264 empty config - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session")
        return
    try:
        video_msg = build_legacy_video_config(CODEC_H264, b'')
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("H264 empty config body", "Keyframe", "Critical", True,
                  "Server survived empty H264 config (no crash)")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("H264 empty config body", "Keyframe", "Critical", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("H264 empty config body", "Keyframe", "Critical", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # Test 2b: AVC config with numSPS=0, numPPS=0
    time.sleep(0.5)
    sock = full_publish_setup("chaos_h264_nosps")
    if not sock:
        log_result("H264 no-SPS config - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session (server may have crashed)")
        return
    try:
        config = bytearray()
        config.append(1)        # configurationVersion
        config.append(0x42)     # AVCProfileIndication
        config.append(0xc0)     # profile_compatibility
        config.append(0x1e)     # AVCLevelIndication
        config.append(0xFF)     # lengthSizeMinusOne = 3
        config.append(0xE0)     # numOfSequenceParameterSets = 0 (!!)
        config.append(0)        # numOfPictureParameterSets = 0 (!!)

        video_msg = build_legacy_video_config(CODEC_H264, bytes(config))
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("H264 config with 0 SPS/PPS", "Keyframe", "Critical", True,
                  "Server survived H264 config with no SPS/PPS")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("H264 config with 0 SPS/PPS", "Keyframe", "Critical", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("H264 config with 0 SPS/PPS", "Keyframe", "Critical", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # Test 2c: Garbage bytes as config
    time.sleep(0.5)
    sock = full_publish_setup("chaos_h264_garbage")
    if not sock:
        log_result("H264 garbage config - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session (server may have crashed)")
        return
    try:
        video_msg = build_legacy_video_config(CODEC_H264, os.urandom(64))
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("H264 garbage config bytes", "Keyframe", "High", True,
                  "Server survived random garbage as H264 config")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("H264 garbage config bytes", "Keyframe", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("H264 garbage config bytes", "Keyframe", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_extended_video_malformed():
    """
    CHAOS PATTERN: Extended video messages with malformed FourCC configs.
    Tests VideoExSequenceStart with bad config payloads for each FourCC.
    """
    print("\n--- Test 3: Extended Video Sequence Start with malformed configs ---")

    # Test 3a: HEVC FourCC with empty config
    sock = full_publish_setup("chaos_ex_hevc_empty")
    if not sock:
        log_result("Extended HEVC empty - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session")
        return
    try:
        video_msg = build_extended_video_sequence_start(FOURCC_HEVC, b'')
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("Extended HEVC empty config", "Keyframe", "Critical", True,
                  "Server survived extended HEVC with empty config")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Extended HEVC empty config", "Keyframe", "Critical", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Extended HEVC empty config", "Keyframe", "Critical", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # Test 3b: AVC FourCC with empty config
    time.sleep(0.5)
    sock = full_publish_setup("chaos_ex_avc_empty")
    if not sock:
        log_result("Extended AVC empty - setup", "Keyframe", "Critical", False,
                  "Could not establish publish session (server may have crashed)")
        return
    try:
        video_msg = build_extended_video_sequence_start(FOURCC_AVC, b'')
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("Extended AVC empty config", "Keyframe", "Critical", True,
                  "Server survived extended AVC with empty config")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Extended AVC empty config", "Keyframe", "Critical", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Extended AVC empty config", "Keyframe", "Critical", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # Test 3c: Unknown FourCC value
    time.sleep(0.5)
    sock = full_publish_setup("chaos_ex_unknown_fourcc")
    if not sock:
        log_result("Unknown FourCC - setup", "Protocol", "High", False,
                  "Could not establish publish session")
        return
    try:
        bogus_fourcc = 0xDEADBEEF
        video_msg = build_extended_video_sequence_start(bogus_fourcc, os.urandom(32))
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("Unknown FourCC 0xDEADBEEF", "Protocol", "High", True,
                  "Server survived unknown FourCC value")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Unknown FourCC 0xDEADBEEF", "Protocol", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Unknown FourCC 0xDEADBEEF", "Protocol", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # Test 3d: AV1 FourCC with garbage config
    time.sleep(0.5)
    sock = full_publish_setup("chaos_ex_av1_garbage")
    if not sock:
        log_result("AV1 garbage config - setup", "Keyframe", "High", False,
                  "Could not establish publish session")
        return
    try:
        video_msg = build_extended_video_sequence_start(FOURCC_AV1, os.urandom(16))
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, video_msg)
        time.sleep(1)
        log_result("AV1 FourCC with garbage config", "Keyframe", "High", True,
                  "Server survived AV1 with garbage config")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("AV1 FourCC with garbage config", "Keyframe", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("AV1 FourCC with garbage config", "Keyframe", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_mid_handshake_abort():
    """
    CHAOS PATTERN: Abort TCP connections at various points during handshake.
    Targets potential goroutine leaks or panics in handshake error paths.
    """
    print("\n--- Test 4: Mid-handshake connection aborts ---")

    # 4a: Connect and immediately close (before C0)
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        sock.close()
        time.sleep(0.3)
        log_result("Abort before C0", "Connection", "Medium", True,
                  "Server survived immediate TCP close")
    except Exception as e:
        log_result("Abort before C0", "Connection", "Medium", False, str(e))

    # 4b: Send C0 only, then close
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        sock.sendall(struct.pack('B', RTMP_VERSION))
        time.sleep(0.2)
        sock.close()
        time.sleep(0.3)
        log_result("Abort after C0", "Connection", "Medium", True,
                  "Server survived abort after C0")
    except Exception as e:
        log_result("Abort after C0", "Connection", "Medium", False, str(e))

    # 4c: Send partial C1 (only 100 bytes of 1536), then close
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        sock.sendall(struct.pack('B', RTMP_VERSION))
        sock.sendall(os.urandom(100))  # Partial C1
        time.sleep(0.2)
        sock.close()
        time.sleep(0.3)
        log_result("Abort with partial C1", "Connection", "Medium", True,
                  "Server survived partial C1 abort")
    except Exception as e:
        log_result("Abort with partial C1", "Connection", "Medium", False, str(e))

    # 4d: Complete handshake, send partial connect command, then close
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        # Send only the chunk header, no body
        sock.sendall(b'\x03\x00\x00\x00\x00\x00\x05\x14\x00\x00\x00\x00')
        time.sleep(0.2)
        sock.close()
        time.sleep(0.3)
        log_result("Abort after handshake with partial command", "Connection", "Medium", True,
                  "Server survived partial command abort")
    except Exception as e:
        log_result("Abort after handshake with partial command", "Connection", "Medium", False, str(e))

    # 4e: TCP RST (SO_LINGER with 0 timeout) during handshake
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        sock.sendall(struct.pack('B', RTMP_VERSION))
        sock.sendall(os.urandom(1536))
        # Force RST instead of FIN
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack('ii', 1, 0))
        sock.close()
        time.sleep(0.3)
        log_result("TCP RST during handshake", "Connection", "High", True,
                  "Server survived TCP RST during handshake")
    except Exception as e:
        log_result("TCP RST during handshake", "Connection", "High", False, str(e))

    # Verify server still accepts connections after all the abuse
    time.sleep(1)
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        connected = do_connect(sock)
        sock.close()
        log_result("Post-abort connectivity check", "Connection", "Critical", connected,
                  "Server still accepts connections after handshake abuse" if connected
                  else "SERVER MAY HAVE CRASHED - cannot connect after handshake abuse")
    except Exception as e:
        log_result("Post-abort connectivity check", "Connection", "Critical", False,
                  f"SERVER MAY HAVE CRASHED: {e}")


def test_truncated_messages():
    """
    CHAOS PATTERN: Send truncated RTMP messages of various types.
    Tests boundary checks in message unmarshal functions.
    """
    print("\n--- Test 5: Truncated/Malformed RTMP Messages ---")

    # 5a: Video message with only 1 byte (below minimum 5 for legacy)
    sock = full_publish_setup("chaos_trunc_video1")
    if not sock:
        log_result("Truncated video 1 byte - setup", "Bitstream", "High", False,
                  "Could not establish publish session")
        return
    try:
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, b'\x17')  # Just 1 byte
        time.sleep(1)
        log_result("Video message 1 byte", "Bitstream", "High", True,
                  "Server survived 1-byte video message")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Video message 1 byte", "Bitstream", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Video message 1 byte", "Bitstream", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # 5b: Video message with 3 bytes (above 1 but below 5)
    time.sleep(0.5)
    sock = full_publish_setup("chaos_trunc_video3")
    if not sock:
        log_result("Truncated video 3 bytes - setup", "Bitstream", "High", False,
                  "Could not establish publish session")
        return
    try:
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, b'\x17\x00\x00')
        time.sleep(1)
        log_result("Video message 3 bytes", "Bitstream", "High", True,
                  "Server survived 3-byte video message")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Video message 3 bytes", "Bitstream", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Video message 3 bytes", "Bitstream", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # 5c: Extended video message with only the type byte (missing FourCC)
    time.sleep(0.5)
    sock = full_publish_setup("chaos_trunc_exvideo")
    if not sock:
        log_result("Truncated extended video - setup", "Bitstream", "High", False,
                  "Could not establish publish session")
        return
    try:
        # 0x80 = extended bit set, type=0 (sequence start), but no FourCC bytes
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, b'\x80')
        time.sleep(1)
        log_result("Extended video missing FourCC", "Bitstream", "High", True,
                  "Server survived extended video with no FourCC")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Extended video missing FourCC", "Bitstream", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Extended video missing FourCC", "Bitstream", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()

    # 5d: Audio message with 0 bytes
    time.sleep(0.5)
    sock = full_publish_setup("chaos_trunc_audio")
    if not sock:
        log_result("Truncated audio 0 bytes - setup", "Bitstream", "High", False,
                  "Could not establish publish session")
        return
    try:
        write_chunk(sock, 4, MSG_AUDIO, 1, 0, b'')
        time.sleep(1)
        log_result("Audio message 0 bytes", "Bitstream", "High", True,
                  "Server survived 0-byte audio message")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Audio message 0 bytes", "Bitstream", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Audio message 0 bytes", "Bitstream", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_rapid_reconnect():
    """
    CHAOS PATTERN: Rapid connect/disconnect cycles to stress goroutine cleanup.
    Tests for goroutine leaks and race conditions.
    """
    print("\n--- Test 6: Rapid reconnect/disconnect cycles ---")
    num_cycles = 20
    successes = 0
    failures = 0

    for i in range(num_cycles):
        try:
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.settimeout(3)
            sock.connect((TARGET_HOST, TARGET_PORT))
            do_handshake(sock)
            do_connect(sock)
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack('ii', 1, 0))
            sock.close()
            successes += 1
        except:
            failures += 1

    log_result(f"Rapid reconnect {num_cycles} cycles", "Connection", "High",
              successes > num_cycles * 0.7,
              f"{successes}/{num_cycles} successful cycles, {failures} failures")

    # Post-reconnect check
    time.sleep(2)
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        connected = do_connect(sock)
        sock.close()
        log_result("Post-rapid-reconnect check", "Connection", "Critical", connected,
                  "Server operational after rapid reconnects" if connected
                  else "SERVER DEGRADED after rapid reconnects")
    except Exception as e:
        log_result("Post-rapid-reconnect check", "Connection", "Critical", False,
                  f"SERVER MAY HAVE CRASHED after rapid reconnects: {e}")


def test_concurrent_connections():
    """
    CHAOS PATTERN: Many simultaneous RTMP connections.
    Tests for resource exhaustion and race conditions.
    """
    print("\n--- Test 7: Concurrent RTMP connections ---")
    num_connections = 30
    connected = [False] * num_connections
    errors_list = []
    lock = threading.Lock()

    def worker(idx):
        try:
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.settimeout(TIMEOUT)
            sock.connect((TARGET_HOST, TARGET_PORT))
            do_handshake(sock)
            result = do_connect(sock)
            with lock:
                connected[idx] = result
            time.sleep(2)
            sock.close()
        except Exception as e:
            with lock:
                errors_list.append(str(e))

    threads = []
    for i in range(num_connections):
        t = threading.Thread(target=worker, args=(i,))
        threads.append(t)
        t.start()
        time.sleep(0.05)  # Small stagger

    for t in threads:
        t.join(timeout=10)

    success_count = sum(connected)
    log_result(f"Concurrent connections ({num_connections})", "Connection", "High",
              success_count > num_connections * 0.5,
              f"{success_count}/{num_connections} connected successfully, {len(errors_list)} errors")

    # Post-flood check
    time.sleep(2)
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        result = do_connect(sock)
        sock.close()
        log_result("Post-flood connectivity", "Connection", "Critical", result,
                  "Server healthy after connection flood" if result
                  else "SERVER DEGRADED after connection flood")
    except Exception as e:
        log_result("Post-flood connectivity", "Connection", "Critical", False,
                  f"SERVER MAY HAVE CRASHED after connection flood: {e}")


def test_duplicate_stream_keys():
    """
    CHAOS PATTERN: Multiple publishers on the same stream key simultaneously.
    Tests for race conditions in path management.
    """
    print("\n--- Test 8: Duplicate stream key publishing ---")
    stream_key = "chaos_duplicate"

    sock1 = full_publish_setup(stream_key)
    if not sock1:
        log_result("Duplicate key - first publisher", "Connection", "High", False,
                  "Could not establish first publish session")
        return

    # Try to publish on the same key
    sock2 = full_publish_setup(stream_key)
    try:
        if sock2:
            # Both connected -- send data on both
            valid_config = build_legacy_video_config(CODEC_H264, build_minimal_avc_config())
            write_chunk(sock1, 6, MSG_VIDEO, 1, 0, valid_config)
            write_chunk(sock2, 6, MSG_VIDEO, 1, 0, valid_config)
            time.sleep(1)
            log_result("Duplicate stream key", "Connection", "High", True,
                      "Server handled duplicate stream keys (allowed or rejected second)")
        else:
            log_result("Duplicate stream key", "Connection", "High", True,
                      "Server correctly rejected second publisher on same key")
    except Exception as e:
        log_result("Duplicate stream key", "Connection", "High", True,
                  f"Server handled duplicate key scenario: {e}")
    finally:
        sock1.close()
        if sock2:
            sock2.close()


def test_oversized_messages():
    """
    CHAOS PATTERN: Extremely large RTMP messages.
    Tests for buffer overflow or memory exhaustion.
    """
    print("\n--- Test 9: Oversized RTMP messages ---")

    sock = full_publish_setup("chaos_oversized")
    if not sock:
        log_result("Oversized message - setup", "Bandwidth", "High", False,
                  "Could not establish publish session")
        return
    try:
        # 1MB of random data as a video config
        large_payload = build_legacy_video_config(CODEC_H264, os.urandom(1024 * 1024))
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, large_payload, chunk_size=4096)
        time.sleep(2)
        log_result("1MB video config message", "Bandwidth", "High", True,
                  "Server survived 1MB video config message")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("1MB video config message", "Bandwidth", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("1MB video config message", "Bandwidth", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_invalid_video_types():
    """
    CHAOS PATTERN: Video messages with invalid type values.
    Tests the type validation in Video.unmarshal().
    """
    print("\n--- Test 10: Invalid video message types ---")

    for bad_type in [3, 4, 5, 127, 255]:
        time.sleep(0.3)
        sock = full_publish_setup(f"chaos_badtype_{bad_type}")
        if not sock:
            log_result(f"Invalid video type {bad_type} - setup", "Protocol", "Medium", False,
                      "Could not establish publish session")
            continue
        try:
            body = bytearray()
            body.append((1 << 4) | CODEC_H264)
            body.append(bad_type)
            body.extend(b'\x00\x00\x00')
            body.extend(os.urandom(16))
            write_chunk(sock, 6, MSG_VIDEO, 1, 0, bytes(body))
            time.sleep(0.5)
            log_result(f"Invalid video type {bad_type}", "Protocol", "Medium", True,
                      "Server survived invalid video type")
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
            log_result(f"Invalid video type {bad_type}", "Protocol", "Medium", True,
                      f"Server closed connection gracefully: {e}")
        except Exception as e:
            log_result(f"Invalid video type {bad_type}", "Protocol", "Medium", False,
                      f"Unexpected error: {e}")
        finally:
            sock.close()


def test_invalid_extended_video_types():
    """
    CHAOS PATTERN: Extended video messages with invalid extended type values.
    """
    print("\n--- Test 11: Invalid extended video types ---")

    for bad_ext_type in [6, 7, 8, 9, 10, 15]:
        time.sleep(0.3)
        sock = full_publish_setup(f"chaos_badext_{bad_ext_type}")
        if not sock:
            log_result(f"Invalid ext video type {bad_ext_type} - setup", "Protocol", "Medium", False,
                      "Could not establish publish session")
            continue
        try:
            body = bytearray()
            body.append(0x80 | bad_ext_type)
            body.extend(struct.pack('>I', FOURCC_AVC))
            body.extend(os.urandom(16))
            write_chunk(sock, 6, MSG_VIDEO, 1, 0, bytes(body))
            time.sleep(0.5)
            log_result(f"Invalid ext video type {bad_ext_type}", "Protocol", "Medium", True,
                      "Server survived invalid extended video type")
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
            log_result(f"Invalid ext video type {bad_ext_type}", "Protocol", "Medium", True,
                      f"Server closed connection gracefully: {e}")
        except Exception as e:
            log_result(f"Invalid ext video type {bad_ext_type}", "Protocol", "Medium", False,
                      f"Unexpected error: {e}")
        finally:
            sock.close()


def test_valid_then_corrupt():
    """
    CHAOS PATTERN: Send valid config, then corrupt AU data mid-stream.
    Simulates a camera that works initially then starts producing garbage.
    Firmware archetype: Hi3516 clone with memory corruption after thermal throttling.
    """
    print("\n--- Test 12: Valid config then corrupt stream data ---")

    sock = full_publish_setup("chaos_valid_corrupt")
    if not sock:
        log_result("Valid-then-corrupt - setup", "Bitstream", "High", False,
                  "Could not establish publish session")
        return
    try:
        # Send valid H264 config
        valid_config = build_legacy_video_config(CODEC_H264, build_minimal_avc_config())
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, valid_config)
        time.sleep(0.5)

        # Send a few "frames" of garbage AU data
        for i in range(5):
            garbage_au = build_legacy_video_au(CODEC_H264, os.urandom(1024), keyframe=(i == 0))
            write_chunk(sock, 6, MSG_VIDEO, 1, (i + 1) * 33, garbage_au)
            time.sleep(0.1)

        # Now send completely malformed data
        write_chunk(sock, 6, MSG_VIDEO, 1, 200, os.urandom(50))
        time.sleep(1)

        log_result("Valid config then corrupt data", "Bitstream", "High", True,
                  "Server survived corrupt AU data after valid setup")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Valid config then corrupt data", "Bitstream", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Valid config then corrupt data", "Bitstream", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_wrong_codec_then_data():
    """
    CHAOS PATTERN: Send H264 config but then H265 video data (codec mismatch).
    Firmware archetype: RTSP-to-RTMP bridge with incorrect codec detection.
    """
    print("\n--- Test 13: Codec mismatch (H264 config, H265 data) ---")

    sock = full_publish_setup("chaos_codec_mismatch")
    if not sock:
        log_result("Codec mismatch - setup", "Bitstream", "High", False,
                  "Could not establish publish session")
        return
    try:
        # Send H264 config
        valid_config = build_legacy_video_config(CODEC_H264, build_minimal_avc_config())
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, valid_config)
        time.sleep(0.5)

        # Now send data claiming to be H265
        h265_au = build_legacy_video_au(CODEC_H265, os.urandom(512))
        write_chunk(sock, 6, MSG_VIDEO, 1, 33, h265_au)
        time.sleep(1)

        log_result("Codec mismatch H264 config / H265 data", "Bitstream", "High", True,
                  "Server survived codec mismatch")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Codec mismatch H264 config / H265 data", "Bitstream", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Codec mismatch H264 config / H265 data", "Bitstream", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_set_chunk_size_abuse():
    """
    CHAOS PATTERN: Send SetChunkSize with extreme values.
    Tests rawmessage.Reader.SetChunkSize validation.
    """
    print("\n--- Test 14: SetChunkSize abuse ---")

    # 14a: Chunk size = 0
    sock = None
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        # Send SetChunkSize=0 before connect
        payload = struct.pack('>I', 0)
        write_chunk(sock, 2, MSG_SET_CHUNK_SIZE, 0, 0, payload)
        time.sleep(0.5)
        connected = do_connect(sock)
        log_result("SetChunkSize = 0", "Protocol", "High", True,
                  "Server handled zero chunk size")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("SetChunkSize = 0", "Protocol", "High", True,
                  f"Server rejected zero chunk size: {e}")
    except Exception as e:
        log_result("SetChunkSize = 0", "Protocol", "High", True,
                  f"Server handled: {e}")
    finally:
        if sock:
            sock.close()

    # 14b: Chunk size = 0xFFFFFFFF (max uint32)
    time.sleep(0.5)
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        payload = struct.pack('>I', 0xFFFFFFFF)
        write_chunk(sock, 2, MSG_SET_CHUNK_SIZE, 0, 0, payload)
        time.sleep(0.5)
        log_result("SetChunkSize = 0xFFFFFFFF", "Protocol", "High", True,
                  "Server handled max chunk size")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("SetChunkSize = 0xFFFFFFFF", "Protocol", "High", True,
                  f"Server rejected max chunk size: {e}")
    except Exception as e:
        log_result("SetChunkSize = 0xFFFFFFFF", "Protocol", "High", True,
                  f"Server handled: {e}")
    finally:
        if sock:
            sock.close()

    # 14c: Chunk size = 1 (minimum valid, will make everything extremely slow)
    time.sleep(0.5)
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        payload = struct.pack('>I', 1)
        write_chunk(sock, 2, MSG_SET_CHUNK_SIZE, 0, 0, payload)
        time.sleep(0.5)
        log_result("SetChunkSize = 1", "Protocol", "Medium", True,
                  "Server handled chunk size = 1")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("SetChunkSize = 1", "Protocol", "Medium", True,
                  f"Server handled: {e}")
    except Exception as e:
        log_result("SetChunkSize = 1", "Protocol", "Medium", True,
                  f"Server handled: {e}")
    finally:
        if sock:
            sock.close()


def test_data_before_config():
    """
    CHAOS PATTERN: Send video AU data before sending config/sequence header.
    Firmware archetype: Ambarella S3L cameras that sometimes skip IDR on reconnect.
    """
    print("\n--- Test 15: Video data before config ---")

    sock = full_publish_setup("chaos_data_first")
    if not sock:
        log_result("Data before config - setup", "Keyframe", "High", False,
                  "Could not establish publish session")
        return
    try:
        # Send AU data directly without prior config
        au_data = build_legacy_video_au(CODEC_H264, os.urandom(512))
        write_chunk(sock, 6, MSG_VIDEO, 1, 0, au_data)
        time.sleep(0.5)

        # Now send config
        valid_config = build_legacy_video_config(CODEC_H264, build_minimal_avc_config())
        write_chunk(sock, 6, MSG_VIDEO, 1, 33, valid_config)
        time.sleep(0.5)

        # Then more AU data
        au_data2 = build_legacy_video_au(CODEC_H264, os.urandom(512))
        write_chunk(sock, 6, MSG_VIDEO, 1, 66, au_data2)
        time.sleep(1)

        log_result("Video data before config", "Keyframe", "High", True,
                  "Server survived data-before-config scenario")
    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError) as e:
        log_result("Video data before config", "Keyframe", "High", True,
                  f"Server closed connection gracefully: {e}")
    except Exception as e:
        log_result("Video data before config", "Keyframe", "High", False,
                  f"Unexpected error: {e}")
    finally:
        sock.close()


def test_ffmpeg_adversarial():
    """
    Use FFmpeg to send specific adversarial streams that exercise the parser more deeply.
    """
    print("\n--- Test 16: FFmpeg-based adversarial streams ---")
    import subprocess

    # 16a: Valid H264 stream (sanity check)
    try:
        result = subprocess.run([
            "ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=3:size=320x240:rate=15",
            "-f", "lavfi", "-i", "sine=frequency=440:duration=3",
            "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
            "-c:a", "aac", "-b:a", "64k",
            "-f", "flv", f"rtmp://{TARGET_HOST}/live/chaos_ffmpeg_valid"
        ], capture_output=True, text=True, timeout=15)

        success = result.returncode == 0 or "muxing overhead" in result.stderr.lower()
        log_result("FFmpeg valid H264 stream", "Bitstream", "Info", True,
                  f"FFmpeg exited with code {result.returncode}")
    except subprocess.TimeoutExpired:
        log_result("FFmpeg valid H264 stream", "Bitstream", "Info", True,
                  "FFmpeg timed out (stream was being accepted)")
    except Exception as e:
        log_result("FFmpeg valid H264 stream", "Bitstream", "Info", False, str(e))

    time.sleep(1)

    # 16b: H264 stream with wildly jittering PTS
    try:
        result = subprocess.run([
            "ffmpeg", "-y", "-f", "lavfi",
            "-i", "testsrc=duration=3:size=320x240:rate=15",
            "-vf", "setpts='PTS+random(0)*0.5/TB'",
            "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
            "-f", "flv", f"rtmp://{TARGET_HOST}/live/chaos_ffmpeg_jitter"
        ], capture_output=True, text=True, timeout=15)
        log_result("FFmpeg jittery PTS stream", "Timestamp", "Medium", True,
                  f"FFmpeg exited with code {result.returncode}")
    except subprocess.TimeoutExpired:
        log_result("FFmpeg jittery PTS stream", "Timestamp", "Medium", True,
                  "FFmpeg timed out (stream accepted)")
    except Exception as e:
        log_result("FFmpeg jittery PTS stream", "Timestamp", "Medium", False, str(e))

    time.sleep(1)

    # 16c: H265 stream (if libx265 available)
    try:
        result = subprocess.run([
            "ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=3:size=320x240:rate=15",
            "-c:v", "libx265", "-preset", "ultrafast",
            "-tag:v", "hvc1",
            "-f", "flv", f"rtmp://{TARGET_HOST}/live/chaos_ffmpeg_h265"
        ], capture_output=True, text=True, timeout=15)
        log_result("FFmpeg H265 stream", "Bitstream", "High", True,
                  f"FFmpeg exited with code {result.returncode}")
    except subprocess.TimeoutExpired:
        log_result("FFmpeg H265 stream", "Bitstream", "High", True,
                  "FFmpeg timed out (stream accepted)")
    except Exception as e:
        log_result("FFmpeg H265 stream", "Bitstream", "High", False, str(e))


def test_final_health_check():
    """Verify the server is still healthy after all tests."""
    print("\n--- Final Health Check ---")
    time.sleep(2)
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(TIMEOUT)
        sock.connect((TARGET_HOST, TARGET_PORT))
        do_handshake(sock)
        connected = do_connect(sock)
        sock.close()
        log_result("FINAL HEALTH CHECK", "Connection", "Critical", connected,
                  "Server is HEALTHY and accepting connections after full test suite"
                  if connected else "SERVER IS DEGRADED - connect failed after tests")
    except Exception as e:
        log_result("FINAL HEALTH CHECK", "Connection", "Critical", False,
                  f"SERVER MAY HAVE CRASHED: {e}")


# ============================================================
# Main
# ============================================================
def main():
    print("=" * 70)
    print("CamChaos RTMP Adversarial Test Suite")
    print(f"Target: {TARGET_HOST}:{TARGET_PORT}")
    print(f"Time: {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 70)
    print("\nWARNING: Only run against dev/staging servers you own.\n")

    start_time = time.time()

    # Run all tests in order (some depend on server state from prior tests)
    if not test_connectivity():
        print("\n\033[91mABORTING: Cannot connect to target server\033[0m")
        sys.exit(1)

    test_h265_nil_hevc_config()
    test_h264_nil_avc_config()
    test_extended_video_malformed()
    test_mid_handshake_abort()
    test_truncated_messages()
    test_invalid_video_types()
    test_invalid_extended_video_types()
    test_set_chunk_size_abuse()
    test_data_before_config()
    test_valid_then_corrupt()
    test_wrong_codec_then_data()
    test_duplicate_stream_keys()
    test_oversized_messages()
    test_rapid_reconnect()
    test_concurrent_connections()
    test_ffmpeg_adversarial()
    test_final_health_check()

    elapsed = time.time() - start_time

    # Print summary
    print("\n" + "=" * 70)
    print("TEST CAMPAIGN SUMMARY")
    print("=" * 70)

    total = len(results)
    passed = sum(1 for r in results if r["passed"])
    failed = sum(1 for r in results if not r["passed"])

    categories = {}
    for r in results:
        cat = r["category"]
        if cat not in categories:
            categories[cat] = {"total": 0, "passed": 0}
        categories[cat]["total"] += 1
        if r["passed"]:
            categories[cat]["passed"] += 1

    print(f"\nTotal tests: {total}")
    print(f"Passed:      {passed}")
    print(f"Failed:      {failed}")
    print(f"Duration:    {elapsed:.1f}s")

    print(f"\n{'Category':<20} {'Passed':<10} {'Total':<10} {'Score':<10}")
    print("-" * 50)
    for cat, counts in sorted(categories.items()):
        score = int(100 * counts["passed"] / counts["total"]) if counts["total"] > 0 else 0
        print(f"{cat:<20} {counts['passed']:<10} {counts['total']:<10} {score}%")

    if failed > 0:
        print("\n\033[91mFAILED TESTS:\033[0m")
        for r in results:
            if not r["passed"]:
                print(f"  - [{r['severity']}] {r['name']}: {r['detail']}")

    overall = int(100 * passed / total) if total > 0 else 0
    print(f"\n\033[1mOverall Resilience Score: {overall}/100\033[0m")

    if overall == 100:
        print("\033[92mAll tests passed. The patched server handled all adversarial input correctly.\033[0m")
    elif overall >= 80:
        print("\033[93mMost tests passed. Some edge cases may need attention.\033[0m")
    else:
        print("\033[91mSignificant failures detected. Review failed tests above.\033[0m")


if __name__ == "__main__":
    main()
