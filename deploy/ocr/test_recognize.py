import unittest

from recognize import ALIASES, NameDecoder, select_name, shape_name


class RecognitionPolicyTests(unittest.TestCase):
    def test_exact_shape_aliases_preserve_distinct_kazakh_letters(self):
        self.assertEqual(shape_name("ИBAH"), "ИВАН")
        for a, b in [("Қ", "К"), ("Ә", "А"), ("Ұ", "У"), ("І", "И")]:
            self.assertNotEqual(shape_name(a), shape_name(b))

    def test_confident_contradiction_requires_review(self):
        self.assertTrue(select_name("ҚАЛИЕВ", .995, "КАЛИЕВ", .999, .98)[2])
        self.assertTrue(select_name("ҚАЛИЕВ", .995, "КАЛИЕВ", .70, .98)[2])
        self.assertEqual(select_name("ИБАH", .81, "ИВАН", .995, .98), ("ИВАН", .995, False))
        self.assertLess(select_name("ИБАH", .81, "ИВАН", .85, .98)[1], .98)

    def test_probability_mass_is_combined_only_for_identical_glyphs(self):
        import numpy as np

        class Decoder:
            dict = {letter: i for i, letter in enumerate("AHАНҚК")}

            def __call__(self, predictions, *args, **kwargs):
                return predictions

        p = np.array([[[.15, .10, .30, .20, .20, .05]]])
        out = NameDecoder(Decoder())(p)
        self.assertAlmostEqual(out.sum(), p.sum())
        self.assertAlmostEqual(out[0, 0, 2], .45)
        self.assertAlmostEqual(out[0, 0, 3], .30)
        self.assertAlmostEqual(out[0, 0, 4], .20)
        self.assertAlmostEqual(out[0, 0, 5], .05)
        self.assertTrue(np.array_equal(p, [[[.15, .10, .30, .20, .20, .05]]]))


if __name__ == "__main__":
    unittest.main()
