<?php

declare(strict_types=1);

namespace ZCourier\Protocol;

use ZCourier\Exception\ProtocolException;

final class Integer64
{
    private const UINT64_MAX = '18446744073709551615';
    private const UINT64_MODULUS = '18446744073709551616';
    private const INT64_MAX = '9223372036854775807';
    private const INT64_MIN_ABS = '9223372036854775808';

    private function __construct()
    {
    }

    public static function normalizeUnsigned(int|string $value): string
    {
        $decimal = self::normalizeDecimal($value, false);
        if (self::compare($decimal, self::UINT64_MAX) > 0) {
            throw new ProtocolException(
                ProtocolException::INVALID_INTEGER,
                "unsigned 64-bit integer is out of range: {$decimal}",
            );
        }
        return $decimal;
    }

    public static function normalizeSigned(int|string $value): string
    {
        $decimal = self::normalizeDecimal($value, true);
        if ($decimal[0] === '-') {
            $absolute = substr($decimal, 1);
            if (self::compare($absolute, self::INT64_MIN_ABS) > 0) {
                throw new ProtocolException(
                    ProtocolException::INVALID_INTEGER,
                    "signed 64-bit integer is out of range: {$decimal}",
                );
            }
            return $decimal;
        }
        if (self::compare($decimal, self::INT64_MAX) > 0) {
            throw new ProtocolException(
                ProtocolException::INVALID_INTEGER,
                "signed 64-bit integer is out of range: {$decimal}",
            );
        }
        return $decimal;
    }

    public static function encodeUnsigned(int|string $value): string
    {
        $decimal = self::normalizeUnsigned($value);
        $bytes = '';
        for ($index = 0; $index < 8; $index++) {
            [$decimal, $remainder] = self::divideBySmall($decimal, 256);
            $bytes = chr($remainder) . $bytes;
        }
        if ($decimal !== '0') {
            throw new ProtocolException(
                ProtocolException::INVALID_INTEGER,
                'unsigned 64-bit integer is out of range',
            );
        }
        return $bytes;
    }

    public static function encodeSigned(int|string $value): string
    {
        $decimal = self::normalizeSigned($value);
        if ($decimal[0] !== '-') {
            return self::encodeUnsigned($decimal);
        }
        $unsigned = self::subtract(self::UINT64_MODULUS, substr($decimal, 1));
        return self::encodeUnsigned($unsigned);
    }

    public static function decodeUnsigned(string $bytes): string
    {
        self::requireEightBytes($bytes);
        $decimal = '0';
        for ($index = 0; $index < 8; $index++) {
            $decimal = self::multiplyAndAdd($decimal, 256, ord($bytes[$index]));
        }
        return $decimal;
    }

    public static function decodeSigned(string $bytes): string
    {
        self::requireEightBytes($bytes);
        $unsigned = self::decodeUnsigned($bytes);
        if ((ord($bytes[0]) & 0x80) === 0) {
            return $unsigned;
        }
        return '-' . self::subtract(self::UINT64_MODULUS, $unsigned);
    }

    private static function normalizeDecimal(int|string $value, bool $signed): string
    {
        $decimal = (string) $value;
        $pattern = $signed ? '/^-?[0-9]+$/' : '/^[0-9]+$/';
        if (preg_match($pattern, $decimal) !== 1) {
            throw new ProtocolException(
                ProtocolException::INVALID_INTEGER,
                "invalid decimal integer: {$decimal}",
            );
        }

        $negative = $decimal[0] === '-';
        $digits = $negative ? substr($decimal, 1) : $decimal;
        $digits = ltrim($digits, '0');
        if ($digits === '') {
            return '0';
        }
        return $negative ? '-' . $digits : $digits;
    }

    private static function compare(string $left, string $right): int
    {
        $lengthComparison = strlen($left) <=> strlen($right);
        return $lengthComparison !== 0 ? $lengthComparison : strcmp($left, $right);
    }

    /** @return array{string, int} */
    private static function divideBySmall(string $decimal, int $divisor): array
    {
        $quotient = '';
        $remainder = 0;
        $length = strlen($decimal);
        for ($index = 0; $index < $length; $index++) {
            $current = ($remainder * 10) + (ord($decimal[$index]) - 48);
            $digit = intdiv($current, $divisor);
            if ($quotient !== '' || $digit !== 0) {
                $quotient .= (string) $digit;
            }
            $remainder = $current % $divisor;
        }
        return [$quotient === '' ? '0' : $quotient, $remainder];
    }

    private static function multiplyAndAdd(string $decimal, int $multiplier, int $addend): string
    {
        $carry = $addend;
        $result = '';
        for ($index = strlen($decimal) - 1; $index >= 0; $index--) {
            $current = ((ord($decimal[$index]) - 48) * $multiplier) + $carry;
            $result = (string) ($current % 10) . $result;
            $carry = intdiv($current, 10);
        }
        while ($carry > 0) {
            $result = (string) ($carry % 10) . $result;
            $carry = intdiv($carry, 10);
        }
        return ltrim($result, '0') ?: '0';
    }

    private static function subtract(string $left, string $right): string
    {
        $borrow = 0;
        $result = '';
        $leftIndex = strlen($left) - 1;
        $rightIndex = strlen($right) - 1;
        while ($leftIndex >= 0) {
            $leftDigit = ord($left[$leftIndex]) - 48 - $borrow;
            $rightDigit = $rightIndex >= 0 ? ord($right[$rightIndex]) - 48 : 0;
            if ($leftDigit < $rightDigit) {
                $leftDigit += 10;
                $borrow = 1;
            } else {
                $borrow = 0;
            }
            $result = (string) ($leftDigit - $rightDigit) . $result;
            $leftIndex--;
            $rightIndex--;
        }
        return ltrim($result, '0') ?: '0';
    }

    private static function requireEightBytes(string $bytes): void
    {
        if (strlen($bytes) !== 8) {
            throw new ProtocolException(
                ProtocolException::INVALID_INTEGER,
                '64-bit integer requires exactly 8 bytes',
            );
        }
    }
}
