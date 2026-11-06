package auth

// empty_stored_test.go — 空存储密码不是「没有密码」，是「任何密码都对」。
//
// 2026-11-07 通宵轮 G10 取证发现的：verifyLegacyOne 先按前缀分流 PHC 家族
// （`$2` / `$argon2`），空串没有那个前缀，于是落到最后一行的
// `hmac.Equal(computeLegacy(method, …), []byte(stored))`；而 computeLegacy 对
// 它不认识的 method（bcrypt / argon2i / php_password_hash 在 legacy 表里没有
// 计算分支，以及任何将来新增的 method 名）返回**空串**。空串 == 空串 ⇒ 通过。
//
// 触发面是存量数据，不是畸形输入：blessing 导入或历史脏行里 password 为空的
// 账号，加上 method 落在那三个里，任何访客都能登进去。

import "testing"

func TestEmptyStoredHashMatchesNothing(t *testing.T) {
	methods := []string{
		MethodMD5, MethodSALTED2MD5, MethodSHA256, MethodSHA512,
		MethodSALTED2SHA256, MethodSALTED2SHA512, MethodBCRYPT, MethodARGON2I,
		MethodPHPDefault, MethodAuthmeSHA256, MethodAuthmeSALTED2MD5,
		MethodAuthmeSALTED2SHA512,
		"some_method_we_do_not_compute", // 未来新增 method 的同一条路
	}
	for _, m := range methods {
		if verifyLegacyOne(m, "hunter2", "salt", "") {
			t.Errorf("verifyLegacyOne(%q, …, stored=\"\") accepted an arbitrary password — empty stored hash is a bypass", m)
		}
		if VerifyPHC("hunter2", "") {
			t.Errorf("VerifyPHC(%q, \"\") accepted an arbitrary password", m)
		}
	}

	// 反向对照：这条守卫不能把正常路径一起打死。期望值是公开已知的
	// md5("password")，不是被测代码自己算出来的（同义反复测试天生就过）。
	if !verifyLegacyOne(MethodMD5, "password", "", "5f4dcc3b5aa765d61d8327deb882cf99") {
		t.Error("a correct non-empty md5 digest stopped matching — the guard is too broad")
	}
}
