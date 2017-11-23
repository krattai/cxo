package registry

import (
	"fmt"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
)

// clear Refs, and set provided degree to the Refs
func clearRefs(
	t *testing.T,
	r *Refs,
	pack Pack,
	degree int,
) {

	var err error

	r.Clear() // clear the Refs making it Refs{}

	if degree != Degree { // if it's not default
		if err = r.SetDegree(pack, degree); err != nil { // change it
			t.Fatal(err)
		}
	}

}

func TestRefs_Append(t *testing.T) {
	// Append(pack Pack, refs *Refs) (err error)

	//

}

func TestRefs_AppendValues(t *testing.T) {
	// AppendValues(pack Pack, values ...interface{}) (err error)

	// TODO (kostyarin): the AppendValues method based on the AppendHashes
	//                   method and this test case is not important a lot,
	//                   thus I mark it as low priority

}

func testRefsAppendHashesCheck(
	t *testing.T, //           : the testing
	r *Refs, //                : the Refs
	pack Pack, //              : the Pack
	shift int, //              : length of the Refs before appending
	hashes []cipher.SHA256, // : appended values
) (
	nl int, //                 : new length
) {

	var err error

	if nl, err = r.Len(pack); err != nil {

		t.Error(err)

	} else if nl != shift+len(hashes) {

		t.Errorf("wrong new length %d, but want %d", nl, shift+len(hashes))

	} else {

		var h cipher.SHA256
		for i, hash := range hashes {
			if h, err = r.HashByIndex(pack, shift+i); err != nil {
				t.Errorf("shift %d, i %d, %s", shift, i, err.Error())
			} else if h != hash {
				t.Error("wrong hash of %d: %s, want %s",
					shift+i,
					h.Hex()[:7],
					hash.Hex()[:7])
			}
		}

	}

	if shift > 0 {
		hashes = append(hashes, hashes...) // stub for now
	}

	logRefsTree(t, r, pack, false)

	return

}

func TestRefs_AppendHashes(t *testing.T) {
	// AppendHashes(pack Pack, hashes ...cipher.SHA256) (err error)

	var (
		pack = getTestPack()

		refs Refs
		err  error

		users []cipher.SHA256 // hashes of the users
	)

	for _, degree := range []int{
		2,
		// Degree,     // default
		// Degree + 7, // changed
	} {

		t.Run(
			fmt.Sprintf("append nothing to blank Refs (degree is %d)", degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree)
				var ln int
				if err = refs.AppendHashes(pack); err != nil {
					t.Error(err)
				} else if ln, err = refs.Len(pack); err != nil {
					t.Error(err)
				} else if ln != 0 {
					t.Error("wrong length")
				}
			})

		var length = 4 // degree*degree + 1

		t.Logf("Refs with %d elements (degree %d)", length, degree)

		pack.ClearFlags(^0) //clear all flags
		clearRefs(t, &refs, pack, degree)

		// generate users
		users = getHashList(getTestUsers(length))

		t.Run(
			fmt.Sprintf("reset-append increasing number of elements %d:%d",
				length,
				degree),
			func(t *testing.T) {

				for k := 0; k <= len(users) && t.Failed() == false; k++ {

					clearRefs(t, &refs, pack, degree) // can call t.Fatal

					if err = refs.AppendHashes(pack, users[:k]...); err != nil {
						t.Fatal(err)
					}

					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k])

				}

			})

		t.Run(fmt.Sprintf("append one by one %d:%d", length, degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree) // can call t.Fatal

				for k := 0; k < len(users) && t.Failed() == false; k++ {

					if err = refs.AppendHashes(pack, users[k]); err != nil {
						t.Fatal(err)
					}

					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k+1])

				}

			})

		t.Run(fmt.Sprintf("append many to full Refs %d:%d", length, degree),
			func(t *testing.T) {

				logRefsTree(t, &refs, pack, false)

				// now the Refs contains all the hashes, let's append them twice
				if err = refs.AppendHashes(pack, users...); err != nil {
					t.Fatal(err)
				}

				testRefsAppendHashesCheck(t, &refs, pack, len(users), users)

			})

		t.Run(fmt.Sprintf("append-reset-append %d:%d", length, degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree)

				for k := 0; k < len(users) && t.Failed() == false; k++ {

					if err = refs.AppendHashes(pack, users[k]); err != nil {
						t.Fatal(err)
					}

					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k+1])

					refs.Reset() // keep degree

				}

			})

		/*t.Run(fmt.Sprintf("load (to blank) %d:%d", length, degree),
			func(t *testing.T) {

				refs.Reset() // reset the refs

				testRefsAppendHashesCheck(t, &refs, pack, 0, users)
				logRefsTree(t, &refs, pack, false)

			})

		t.Run(fmt.Sprintf("load entire (to blank) %d:%d", length, degree),
			func(t *testing.T) {

				refs.Reset()              // reset the refs
				pack.AddFlags(EntireRefs) // load entire Refs

				testRefsAppendHashesCheck(t, &refs, pack, 0, users)
				logRefsTree(t, &refs, pack, false)

			})

		t.Run(
			fmt.Sprintf("hash table index (to blank) %d:%d",
				length,
				degree,
			),
			func(t *testing.T) {

				refs.Reset()

				pack.ClearFlags(EntireRefs)
				pack.AddFlags(HashTableIndex)

				testRefsAppendHashesCheck(t, &refs, pack, 0, users)
				logRefsTree(t, &refs, pack, false)

			})*/

	}

	t.Run("blank hash", func(t *testing.T) {

		clearRefs(t, &refs, pack, Degree)

		var hashes = []cipher.SHA256{
			{}, // the blank one
			{}, // the blank two
		}

		if err = refs.AppendHashes(pack, hashes...); err != nil {
			t.Fatal(err)
		}

		testRefsAppendHashesCheck(t, &refs, pack, 0, hashes)

	})

}
